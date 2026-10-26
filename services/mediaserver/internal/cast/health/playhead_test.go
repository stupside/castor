package health

import (
	"context"
	"testing"
	"time"
)

type scriptedHead struct {
	at      time.Duration
	playing bool
	held    bool
}

func (s *scriptedHead) Position(context.Context) (time.Duration, bool, bool) {
	return s.at, s.held, s.playing
}

func TestPlayTrailMeasuresPaceFromThePlayhead(t *testing.T) {
	head := &scriptedHead{playing: true}
	trail := newPlayTrail(head)
	now := time.Now()
	var pace float64
	var known bool
	for i := 0; i <= 6; i++ {
		head.at = time.Duration(i) * headEvery / 2
		pace, known = trail.measure(t.Context(), now.Add(time.Duration(i)*headEvery), false)
	}
	if !known || pace < 0.49 || pace > 0.51 {
		t.Fatalf("pace = %v, %v; want a device playing half real time", pace, known)
	}
}

func TestPlayTrailForgetsWhenHeldOrSilent(t *testing.T) {
	head := &scriptedHead{playing: true}
	trail := newPlayTrail(head)
	now := time.Now()
	for i := 0; i <= 6; i++ {
		head.at = time.Duration(i) * headEvery
		trail.measure(t.Context(), now.Add(time.Duration(i)*headEvery), false)
	}
	head.playing = false
	if _, known := trail.measure(t.Context(), now.Add(6*headEvery+paceSpan), false); known {
		t.Error("a pace older than the span still stands")
	}
	if _, known := trail.measure(t.Context(), now.Add(7*headEvery), true); known {
		t.Error("a held device has a pace")
	}
}
