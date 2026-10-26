package execute

import (
	"cmp"
	"sync"
	"time"
)

const (
	// firstWarmth is how long an encoder is thought to take to make its first byte before one has been seen.
	firstWarmth = 2 * time.Second

	// cutAttempts is how many cuts, each taken further on, a change tries before it gives up.
	cutAttempts = 4
)

// warmth is how long this delivery's encoders took to make their first byte, which is how far ahead of the one running a cut must be taken.
type warmth struct {
	mu   sync.Mutex
	took time.Duration
}

func (w *warmth) note(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.took = max(d, (w.took+d)/2)
}

// margin is how far past the running encoder a cut goes: a new encoder's warm-up, four times more after each miss.
func (w *warmth) margin(src *pull, attempt int) int64 {
	w.mu.Lock()
	took := cmp.Or(w.took, firstWarmth)
	w.mu.Unlock()
	rate := int64(leadFloor / 2)
	if made := src.Progress(); made.Position > 0 && made.Bytes > 0 {
		rate = int64(float64(made.Bytes) / made.Position.Seconds())
	}
	return max(cutMargin, int64(took.Seconds()*1.5*float64(rate))) << (2 * attempt)
}
