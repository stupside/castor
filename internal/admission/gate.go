// Package admission lets work in while a lifetime lasts, up to a capacity, and joins what it let in once that lifetime ends.
package admission

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"golang.org/x/sync/semaphore"
)

// ErrFull refuses work over the gate's capacity.
var ErrFull = errors.New("capacity exhausted")

// Gate lets work in while life lasts, at most capacity at once.
type Gate struct {
	life     context.Context
	capacity int64
	slots    *semaphore.Weighted
}

// New is a gate open while life lasts, for capacity works at once.
func New(life context.Context, capacity int64) *Gate {
	return &Gate{life: life, capacity: capacity, slots: semaphore.NewWeighted(capacity)}
}

// Take lets one work in until release; it refuses with life's cause once life ended, and with ErrFull at capacity.
func (g *Gate) Take() (release func(), err error) {
	if g.life.Err() != nil {
		return nil, context.Cause(g.life)
	}
	if !g.slots.TryAcquire(1) {
		return nil, ErrFull
	}
	// Life may have ended while the slot was taken; what Wait has joined never starts.
	if g.life.Err() != nil {
		g.slots.Release(1)
		return nil, context.Cause(g.life)
	}
	return func() { g.slots.Release(1) }, nil
}

// Enter lets one work in under ctx, as Take does; working also ends, with life's cause, once life ends.
func (g *Gate) Enter(ctx context.Context) (working context.Context, leave func(), err error) {
	release, err := g.Take()
	if err != nil {
		return nil, nil, err
	}
	working, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(g.life, func() { cancel(context.Cause(g.life)) })
	return working, func() {
		stop()
		cancel(nil)
		release()
	}, nil
}

// Answer is what work that failed with err under working answers, before it leaves: the shutdown once life cut it off, its caller's end once the caller gave up, else err.
func (g *Gate) Answer(working context.Context, err error) error {
	if g.life.Err() != nil && errors.Is(context.Cause(working), context.Cause(g.life)) {
		return connect.NewError(connect.CodeUnavailable, context.Cause(g.life))
	}
	if working.Err() != nil {
		return working.Err()
	}
	return err
}

// Wait joins every work let in, once life ended, or until ctx ends.
func (g *Gate) Wait(ctx context.Context) error {
	if err := g.slots.Acquire(ctx, g.capacity); err != nil {
		return err
	}
	g.slots.Release(g.capacity)
	return nil
}
