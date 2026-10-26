package splice

import (
	"errors"
	"testing"
	"time"
)

// bytes framing: 'K' opens a keyframe, 'p' a picture and 's' a sound, each at a second per byte.
type bytesFraming struct{}

func (bytesFraming) Scanner() Scanner                      { return &bytesScanner{} }
func (bytesFraming) Trimmer([]byte, time.Duration) Trimmer { return nil }

type bytesScanner struct{ read int64 }

func (s *bytesScanner) Scan(p []byte, found func(Event)) {
	for _, b := range p {
		at := time.Duration(s.read) * time.Second
		switch b {
		case 'K':
			found(Event{Kind: Keyframe, Offset: s.read, PTS: at, Tables: []byte("T")})
		case 'p':
			found(Event{Kind: Picture, Offset: s.read, PTS: at})
		case 's':
			found(Event{Kind: Sound, Offset: s.read, PTS: at})
		}
		s.read++
	}
}

func TestAnIndexListsWhatAnyFramingReports(t *testing.T) {
	x := NewIndex(bytesFraming{})
	_, _ = x.Write([]byte("Kpp"))
	_, _ = x.Write([]byte("sKp"))
	cut, ok := x.After(1)
	if !ok || cut.Offset != 4 || cut.PTS != 4*time.Second || string(cut.Tables) != "T" {
		t.Fatalf("cut after 1 = %+v (%t), want the keyframe at 4s with its tables", cut, ok)
	}
	if got := x.Extent(); got != 5*time.Second {
		t.Errorf("extent = %v, want 5s: from the first keyframe to the last picture", got)
	}
	if pts, ok := x.SoundAfter(2); !ok || pts != 3*time.Second {
		t.Errorf("sound after 2 = %v (%t), want 3s", pts, ok)
	}
}

func TestAReaderIsAtTheLastFrameThatOpensBeforeWhereItRead(t *testing.T) {
	x := NewIndex(bytesFraming{})
	_, _ = x.Write([]byte("KppsKp"))
	if _, ok := x.At(0); ok {
		t.Error("a reader that read nothing is at a frame")
	}
	for offset, want := range map[int64]time.Duration{1: 0, 4: 3 * time.Second, 6: 4 * time.Second} {
		if got, ok := x.At(offset); !ok || got != want {
			t.Errorf("at %d = %v (%t), want %v", offset, got, ok, want)
		}
	}
}

func TestAnIndexOfAStreamThatEndsWithoutACutSaysSo(t *testing.T) {
	x := NewIndex(bytesFraming{})
	_, _ = x.Write([]byte("pp"))
	x.Close()
	if _, err := x.Wait(t.Context(), 0); !errors.Is(err, ErrNoCut) {
		t.Errorf("wait = %v, want ErrNoCut", err)
	}
}
