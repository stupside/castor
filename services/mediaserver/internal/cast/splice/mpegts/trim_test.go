package mpegts

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/cast/splice"
)

func spoolOf(t *testing.T, data []byte) *storage.Spool {
	t.Helper()
	sp, err := storage.NewSpool(filepath.Join(t.TempDir(), "spool.ts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sp.Close() })
	if _, err := sp.Write(data); err != nil {
		t.Fatal(err)
	}
	return sp
}

func TestACursorCarryingOnFromAnotherSkipsTheSoundFramesBeforeWhereItsSoundReaches(t *testing.T) {
	tables := append(pat(), pmt()...)
	var data []byte
	for i := range 6 {
		data = append(data, soundAt(time.Duration(i)*250*time.Millisecond)...)
		data = append(data, frame(time.Duration(i)*250*time.Millisecond, i == 0)...)
	}
	c, err := splice.Open(t.Context(), spoolOf(t, data), 0, tables, Framing{}.Trimmer(tables, 500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Stop(int64(len(data)))
	// Odd sizes, so a packet can end between two reads.
	var got []byte
	buf := make([]byte, 100)
	for {
		n, err := c.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}
	var sounds, pictures int
	var first time.Duration
	for at := len(tables); at+packetSize <= len(got); at += packetSize {
		switch pid := int(got[at+1]&0x1f)<<8 | int(got[at+2]); pid {
		case audioStream:
			if pts, ok := presentation(got[at : at+packetSize]); ok {
				if sounds == 0 {
					first = pts
				}
				sounds++
			}
		case videoStream:
			pictures++
		}
	}
	if pictures != 6 || sounds != 4 || first != 500*time.Millisecond {
		t.Errorf("got %d pictures and %d frames of sound from %v, want 6 pictures and the 4 frames from 500ms", pictures, sounds, first)
	}
}
