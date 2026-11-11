package fcast

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestPacketsAreFramedAsReceiversReadThem(t *testing.T) {
	near, far := net.Pipe()
	ch := newChannel(near, func(opcode, []byte) {})
	t.Cleanup(func() { _ = ch.Close() })
	t.Cleanup(func() { _ = far.Close() })
	go func() {
		_ = ch.sendJSON(t.Context(), opVersion, versionMessage{Version: 3})
		_ = ch.send(t.Context(), opPause, nil)
	}()
	// A little-endian size that counts the opcode, then the opcode, then the body.
	want := append([]byte{14, 0, 0, 0, byte(opVersion)}, `{"version":3}`...)
	want = append(want, 1, 0, 0, 0, byte(opPause))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(far, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("wire = % x, want % x", got, want)
	}
}

func TestAPacketIsReadUpToTheProtocolsSizeAndNoFurther(t *testing.T) {
	for _, tt := range []struct {
		name   string
		size   uint32
		closes bool
	}{
		{"an empty packet has no opcode", 0, true},
		{"the largest packet the protocol allows", maxPacket, false},
		{"one byte over the protocol's limit", maxPacket + 1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			near, far := net.Pipe()
			observed := make(chan int, 1)
			ch := newChannel(near, func(_ opcode, body []byte) { observed <- len(body) })
			t.Cleanup(func() { _ = ch.Close() })
			t.Cleanup(func() { _ = far.Close() })
			go func() {
				packet := binary.LittleEndian.AppendUint32(nil, tt.size)
				if tt.size > 0 {
					packet = append(packet, make([]byte, tt.size)...)
					packet[4] = byte(opPlaybackUpdate)
				}
				_, _ = far.Write(packet)
			}()
			select {
			case <-ch.gone:
				if !tt.closes {
					t.Error("a packet the protocol allows closed the connection")
				}
			case n := <-observed:
				if tt.closes {
					t.Errorf("a %d byte packet was read as a %d byte body", tt.size, n)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the packet was neither read nor refused")
			}
		})
	}
}

func TestAPacketTooLongForTheProtocolIsRefusedNotCut(t *testing.T) {
	near, far := net.Pipe()
	ch := newChannel(near, func(opcode, []byte) {})
	t.Cleanup(func() { _ = ch.Close() })
	t.Cleanup(func() { _ = far.Close() })
	go func() { _, _ = io.Copy(io.Discard, far) }()
	if err := ch.send(t.Context(), opPlay, make([]byte, maxPacket-1)); err != nil {
		t.Errorf("a body filling the protocol's packet was refused: %v", err)
	}
	if err := ch.send(t.Context(), opPlay, make([]byte, maxPacket)); err == nil {
		t.Error("a body one byte past the protocol's packet was sent")
	}
}

func TestAReceiversPingIsAnsweredWithPong(t *testing.T) {
	_, r, err := connectReceiver(t, 3, greets(nil))
	if err != nil {
		t.Fatal(err)
	}
	// flush fails unless castor answers the receiver's ping.
	r.flush(t)
}

func TestASilentReceiverIsDroppedAfterThreeMissedBeats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		near, far := net.Pipe()
		// Reads castor's pings and never answers them.
		go func() { _, _ = io.Copy(io.Discard, far) }()
		ch := newChannel(near, func(opcode, []byte) {})
		t.Cleanup(func() { _ = ch.Close() })
		t.Cleanup(func() { _ = far.Close() })
		time.Sleep(heartbeatMisses * heartbeatEvery)
		select {
		case <-ch.gone:
			t.Fatal("a receiver was dropped before missing three beats")
		default:
		}
		time.Sleep(heartbeatEvery + time.Second)
		select {
		case <-ch.gone:
		default:
			t.Fatal("a receiver silent for four beats is still connected, so a cast on a TV switched off never ends")
		}
	})
}

func TestAReceiverAnsweringPingsStaysConnected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dev, _, err := connectReceiver(t, 3, greets(nil))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Minute)
		select {
		case <-dev.ch.gone:
			t.Fatal("a receiver answering every ping was dropped")
		default:
		}
	})
}

func TestAWriteThatCannotFinishDoesNotStopReading(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		near, far := net.Pipe()
		observed := make(chan opcode, 1)
		ch := newChannel(near, func(op opcode, _ []byte) { observed <- op })
		t.Cleanup(func() { _ = ch.Close() })
		t.Cleanup(func() { _ = far.Close() })
		// Nothing reads the receiver's end, so this write holds the writer.
		go func() { _ = ch.send(t.Context(), opPlay, []byte(`{}`)) }()
		synctest.Wait()
		go func() {
			const body = `{"state":1}`
			packets := append(binary.LittleEndian.AppendUint32(nil, 1), byte(opPing))
			packets = append(binary.LittleEndian.AppendUint32(packets, 1+uint32(len(body))), byte(opPlaybackUpdate))
			_, _ = far.Write(append(packets, body...))
		}()
		select {
		case op := <-observed:
			if op != opPlaybackUpdate {
				t.Errorf("observed opcode %d, want the update behind the ping", op)
			}
		case <-time.After(time.Second):
			t.Fatal("answering a ping waited on a stalled write, so nothing more was read")
		}
	})
}

type writingConn struct {
	net.Conn
	started chan struct{}
	once    sync.Once
}

func (c *writingConn) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.started) })
	return c.Conn.Write(p)
}

func TestCancellingABlockedWriteLeavesTheChannelUsable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		near, far := net.Pipe()
		writing := &writingConn{Conn: near, started: make(chan struct{})}
		ch := newChannel(writing, func(opcode, []byte) {})
		t.Cleanup(func() { _ = ch.Close() })
		t.Cleanup(func() { _ = far.Close() })
		ctx, cancel := context.WithCancelCause(t.Context())
		cause := errors.New("play abandoned")
		result := make(chan error, 1)
		go func() { result <- ch.send(ctx, opPlay, []byte(`{}`)) }()
		// Nothing reads the peer, so sending cannot complete before cancellation.
		<-writing.started
		cancel(cause)
		select {
		case err := <-result:
			if !errors.Is(err, cause) {
				t.Fatalf("send = %v, want cancellation cause %v", err, cause)
			}
		case <-time.After(time.Second):
			t.Fatal("cancellation did not interrupt the write")
		}
		// A cancelled sender must not leave a deadline on the next write.
		received := make(chan []byte, 1)
		go func() {
			header := make([]byte, 5)
			_, _ = io.ReadFull(far, header)
			received <- header
		}()
		if err := ch.send(t.Context(), opPause, nil); err != nil {
			t.Fatalf("send after cancellation = %v", err)
		}
		if got := <-received; got[4] != byte(opPause) {
			t.Errorf("after cancellation the receiver read % x, want a Pause", got)
		}
	})
}

func TestCancellingAPartialPacketClosesTheConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		near, far := net.Pipe()
		ch := newChannel(near, func(opcode, []byte) {})
		t.Cleanup(func() { _ = ch.Close() })
		t.Cleanup(func() { _ = far.Close() })
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- ch.send(ctx, opPlay, []byte(`{"container":"video/mp4"}`)) }()
		var prefix [4]byte
		if _, err := io.ReadFull(far, prefix[:]); err != nil {
			t.Fatal(err)
		}
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("partial send = %v, want cancelled", err)
			}
		case <-time.After(time.Second):
			t.Fatal("partial send ignored cancellation")
		}
		select {
		case <-ch.gone:
		case <-time.After(time.Second):
			t.Fatal("a partial packet left an unusable connection open")
		}
	})
}
