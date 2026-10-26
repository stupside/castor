package progressive

import (
	"sync"
	"time"
)

// streamAudience owns the changing observations of concurrent HTTP readers.
// Producer ownership and server configuration never change with these observations.
type streamAudience struct {
	mu             sync.Mutex
	active         int
	completed      bool
	lastDisconnect time.Time
	sent           int64
	lastFetch      time.Time
	// gen is the current run; a client of an earlier one is not credited.
	gen int
}

func (a *streamAudience) joined() func(gen int, reachedEOF bool) {
	a.mu.Lock()
	a.active++
	a.mu.Unlock()
	return func(gen int, reachedEOF bool) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.active--
		a.completed = a.completed || reachedEOF && gen == a.gen
		a.lastDisconnect = time.Now()
	}
}

func (a *streamAudience) record(gen int, sent int64, at time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if gen != a.gen {
		return
	}
	a.sent = max(a.sent, sent)
	a.lastFetch = at
}

// restart begins the next run, forgetting what clients took of the last.
func (a *streamAudience) restart() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gen++
	a.sent, a.completed, a.lastFetch = 0, false, time.Now()
	return a.gen
}

func (a *streamAudience) handed() (int64, time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sent, a.lastFetch
}

func (a *streamAudience) settled(grace time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active == 0 && (a.completed || time.Since(a.lastDisconnect) > grace)
}
