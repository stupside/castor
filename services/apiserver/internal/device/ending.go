package device

import (
	"context"
	"sync"
)

// Ending is how one playback ended, for a family whose device says so itself rather than being polled.
// A family finishes it when its protocol reports the end, and the cast waits on it with AwaitEnding.
type Ending struct {
	once sync.Once
	done chan struct{}
	err  error
}

func NewEnding() *Ending { return &Ending{done: make(chan struct{})} }

// Finish records how the playback ended, nil for a clean end. The first call decides; later ones change nothing.
func (e *Ending) Finish(err error) {
	e.once.Do(func() {
		e.err = err
		close(e.done)
	})
}

// Finished closes once the playback ended. A playback never begun has no Ending, which never ends.
func (e *Ending) Finished() <-chan struct{} {
	if e == nil {
		return nil
	}
	return e.done
}

// Err is how the playback ended, read once Finished has closed.
func (e *Ending) Err() error { return e.err }

// AwaitEnding waits for e to finish, for the device's connection to close, or for ctx to end.
// A connection that closed first names the device gone, as observed says, unless the end was read before it closed.
func AwaitEnding(ctx context.Context, e *Ending, gone <-chan struct{}, name, observed string) error {
	select {
	case <-e.Finished():
		return e.err
	case <-gone:
		select {
		case <-e.Finished():
			return e.err
		default:
		}
		return &Gone{Device: name, Observed: observed}
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
