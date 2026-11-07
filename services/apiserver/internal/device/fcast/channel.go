package fcast

import (
	"context"
	"encoding/binary"
	"encoding/json/v2"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// opcode names a v3 packet; castor uses only these.
type opcode byte

const (
	opPlay           opcode = 1
	opPause          opcode = 2
	opResume         opcode = 3
	opStop           opcode = 4
	opPlaybackUpdate opcode = 6
	opPlaybackError  opcode = 9
	opVersion        opcode = 11
	opPing           opcode = 12
	opPong           opcode = 13
	opInitial        opcode = 14
	opPlayUpdate     opcode = 15
	opSubscribeEvent opcode = 17
	opEvent          opcode = 19
)

const (
	// A v3 packet, opcode included, is at most this long; Android receivers drop the connection over it.
	maxPacket = 32000

	// answerWithin bounds a write, the handshake, and the receiver's echo of a Play.
	answerWithin = 10 * time.Second

	// A receiver heard from in none of heartbeatMisses beats is gone, however open its socket looks.
	heartbeatEvery  = 5 * time.Second
	heartbeatMisses = 3
)

// channel is one FCast connection: its reader alone delivers what arrives, so closing it never races a send.
type channel struct {
	conn    net.Conn
	observe func(op opcode, body []byte)
	writing chan struct{}
	// pongs asks the heartbeat goroutine to answer a ping, so a stalled write never stalls reading.
	pongs chan struct{}

	// heard is when the receiver last sent anything, in unix nanoseconds.
	heard atomic.Int64

	gone      chan struct{}
	closeConn func() error
}

// newChannel starts reading conn; observe sees every packet but Ping and Pong, from the reader's goroutine.
func newChannel(conn net.Conn, observe func(opcode, []byte)) *channel {
	ch := &channel{conn: conn, observe: observe, writing: make(chan struct{}, 1), pongs: make(chan struct{}, 1), gone: make(chan struct{})}
	ch.closeConn = sync.OnceValue(conn.Close)
	ch.heard.Store(time.Now().UnixNano())
	go ch.read()
	go ch.heartbeat()
	return ch
}

// heartbeat answers the receiver's pings, pings it every beat, and drops the connection once it has said nothing for heartbeatMisses of them.
func (ch *channel) heartbeat() {
	ticker := time.NewTicker(heartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ch.gone:
			return
		case <-ch.pongs:
			_ = ch.send(context.Background(), opPong, nil)
			continue
		case <-ticker.C:
		}
		if time.Since(time.Unix(0, ch.heard.Load())) > heartbeatMisses*heartbeatEvery {
			_ = ch.closeConn()
			return
		}
		_ = ch.send(context.Background(), opPing, nil)
	}
}

func (ch *channel) read() {
	defer close(ch.gone)
	defer ch.closeConn()
	for {
		op, body, err := ch.receive()
		if err != nil {
			return
		}
		ch.heard.Store(time.Now().UnixNano())
		switch op {
		case opPing:
			// A receiver ends the session when its ping goes unanswered; pings waiting together share one answer.
			select {
			case ch.pongs <- struct{}{}:
			default:
			}
		case opPong:
		default:
			ch.observe(op, body)
		}
	}
}

// receive reads one packet: a little-endian size that counts the opcode, the opcode, then the body.
func (ch *channel) receive() (opcode, []byte, error) {
	var size uint32
	if err := binary.Read(ch.conn, binary.LittleEndian, &size); err != nil {
		return 0, nil, err
	}
	if size == 0 || size > maxPacket {
		return 0, nil, fmt.Errorf("fcast packet of %d bytes is outside the protocol's 1 to %d", size, maxPacket)
	}
	packet := make([]byte, size)
	if _, err := io.ReadFull(ch.conn, packet); err != nil {
		return 0, nil, err
	}
	return opcode(packet[0]), packet[1:], nil
}

// sendJSON sends message as op's JSON body.
func (ch *channel) sendJSON(ctx context.Context, op opcode, message any) error {
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return ch.send(ctx, op, body)
}

// send writes one packet; a body too long for the protocol is refused, never cut.
func (ch *channel) send(ctx context.Context, op opcode, body []byte) error {
	size := 1 + len(body)
	if size > maxPacket {
		return fmt.Errorf("fcast packet of %d bytes exceeds the protocol's %d", size, maxPacket)
	}
	ctx, cancel := context.WithTimeout(ctx, answerWithin)
	defer cancel()
	packet := append(binary.LittleEndian.AppendUint32(make([]byte, 0, 4+size), uint32(size)), byte(op))
	packet = append(packet, body...)
	select {
	case ch.writing <- struct{}{}:
		defer func() { <-ch.writing }()
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	deadline, _ := ctx.Deadline()
	if err := ch.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	// Cancellation must interrupt a blocked write, and its callback must finish
	// before the next sender installs its own deadline.
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = ch.conn.SetWriteDeadline(time.Now())
		close(interrupted)
	})
	defer func() {
		if !stop() {
			<-interrupted
		}
		_ = ch.conn.SetWriteDeadline(time.Time{})
	}()
	n, err := ch.conn.Write(packet)
	if n > 0 && n < len(packet) {
		// A partial packet cannot be retried on this connection.
		_ = ch.closeConn()
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return err
}

// Close returns once the reader has stopped, so nothing is observed after it.
func (ch *channel) Close() error {
	err := ch.closeConn()
	<-ch.gone
	return err
}
