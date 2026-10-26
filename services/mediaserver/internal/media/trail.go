package media

import "time"

// Trail is a growing quantity's samples over the last Span, oldest first, which tell the rate it grows at.
type Trail struct {
	Span    time.Duration
	samples []sample
}

type sample struct {
	at    time.Time
	value float64
}

// Add records value at now and forgets the samples the span no longer reaches.
func (t *Trail) Add(now time.Time, value float64) {
	t.samples = append(t.samples, sample{at: now, value: value})
	for len(t.samples) > 1 && now.Sub(t.samples[1].at) >= t.Span {
		t.samples = t.samples[1:]
	}
}

// Rate is the growth per second over the trail, known once it covers share of Span.
func (t *Trail) Rate(share float64) (float64, bool) {
	if len(t.samples) < 2 {
		return 0, false
	}
	oldest, newest := t.samples[0], t.samples[len(t.samples)-1]
	span := newest.at.Sub(oldest.at)
	if span <= 0 || span < time.Duration(share*float64(t.Span)) {
		return 0, false
	}
	return (newest.value - oldest.value) / span.Seconds(), true
}

func (t *Trail) Reset() { t.samples = nil }
