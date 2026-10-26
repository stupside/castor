package media

import (
	"testing"
	"time"
)

func TestARateIsKnownOnceItsShareOfTheSpanIsCovered(t *testing.T) {
	trail := Trail{Span: 10 * time.Second}
	start := time.Now()
	at := func(seconds int) time.Time { return start.Add(time.Duration(seconds) * time.Second) }

	trail.Add(at(0), 0)
	if _, known := trail.Rate(0); known {
		t.Fatal("a rate was known from a single sample")
	}
	trail.Add(at(2), 2_000_000)
	if _, known := trail.Rate(0.5); known {
		t.Fatal("a rate was known before half a span was covered")
	}
	trail.Add(at(5), 5_000_000)
	if rate, known := trail.Rate(0.5); !known || rate != 1_000_000 {
		t.Errorf("rate = %v (%t), want 1 MB/s once half a span is covered", rate, known)
	}
}

func TestARateFollowsTheLastSpanOnly(t *testing.T) {
	trail := Trail{Span: 10 * time.Second}
	start := time.Now()
	var bytes float64
	// A fast first half minute, then a slow link.
	for s := range 42 {
		if s <= 30 {
			bytes += 4_000_000
		} else {
			bytes += 100_000
		}
		trail.Add(start.Add(time.Duration(s)*time.Second), bytes)
	}
	if rate, known := trail.Rate(0.5); !known || rate > 200_000 {
		t.Errorf("rate = %v (%t), want only the last span's 100 kB/s, not the fast start", rate, known)
	}
}

func TestAQuantityThatStoodStillForASpanGrowsAtNothing(t *testing.T) {
	trail := Trail{Span: 10 * time.Second}
	start := time.Now()
	trail.Add(start, 1_000_000)
	trail.Add(start.Add(8*time.Second), 1_000_000)
	if rate, known := trail.Rate(0.5); !known || rate != 0 {
		t.Errorf("rate = %v (%t), want a quantity that stood still to be known to grow at nothing, not unknown", rate, known)
	}
}
