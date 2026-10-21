package execute

import (
	"context"
	"errors"
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
)

// Preparation registers releases; the coordinator takes ownership after joining it.
// All workers and publications are joined before any storage is released.
// Passive resources are then released in reverse acquisition order.
type releases struct {
	stack   []func() error
	joins   []func() error
	signals []func()
}

func (r *releases) push(release func() error) {
	r.stack = append(r.stack, release)
}

// interrupt registers a nonblocking stop request, sent before any joins.
func (r *releases) interrupt(stop func()) { r.signals = append(r.signals, stop) }

// worker registers a stop request and its matching join as one acquisition.
// A nil stop means the worker observes the attempt context directly.
func (r *releases) worker(stop func(), join func() error) {
	if stop != nil {
		r.interrupt(stop)
	}
	r.joins = append(r.joins, join)
}

// publication starts unpublishing alongside other stop requests. All requests
// must return before the scope releases any storage they could borrow.
func (r *releases) publication(close func() error) {
	done := make(chan error, 1)
	r.worker(func() { go func() { done <- close() }() }, func() error { return <-done })
}

func (r *releases) signal() {
	signals := r.signals
	r.signals = nil
	for _, stop := range signals {
		stop()
	}
}

func (r *releases) release() error {
	r.signal()
	joins, stack := r.joins, r.stack
	r.joins, r.stack = nil, nil
	var errs []error
	for _, join := range slices.Backward(joins) {
		errs = append(errs, join())
	}
	for _, release := range slices.Backward(stack) {
		errs = append(errs, release())
	}
	return errors.Join(errs...)
}

// join ends every coordinator operation, including preparation. Resource
// cleanup cannot race a late acquisition or an outstanding device command.
func (c *coordinator) join() error {
	c.stop(nil)
	// Preparation is the only operation allowed to register resources. Join it
	// before taking ownership of its partial acquisitions and sending stop signals.
	if c.preparation != nil {
		<-c.preparation.done
	}
	c.releases.signal()
	err := c.cause
	for _, op := range c.operations {
		<-op.done
		if op == c.terminal {
			err = errors.Join(err, op.err)
		} else {
			err = errors.Join(err, unlessTeardown(op.err))
		}
		if op.event == deliverySettled && op.err == nil {
			c.delivered = true
		}
	}
	return err
}

func (c *coordinator) finish() (ran, error) {
	err := c.join()
	p := c.presentation
	if err == nil && c.interruption == nil && p.delivery != nil {
		if c.delivered {
			err = p.delivery.sink.Settled(p.delivery.output.Progress())
		} else if c.starvation != nil {
			err = c.starvation
		}
	}
	err = errors.Join(err, c.releases.release())
	// This is the final transition: its error must join the returned outcome,
	// not be stored in c.cause after join has already collected it.
	err = errors.Join(err, fireAttempt(c.ctx, c.machine, cleanupFinished))
	if c.interruption != nil {
		err = &recovery.Interrupted{Cause: c.interruption, Failure: unlessCancellation(err, c.interruption)}
	}
	return p.reached, err
}

// Ignore cancellation caused by joining workers, but retain failures joined to it.
func unlessTeardown(err error) error {
	return unlessCancellation(err, context.Canceled)
}

// A canceled worker can return the parent's custom cause instead of ctx.Err().
// Keep independent failures joined to it, while the interruption owns its cause.
func unlessCancellation(err, cause error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var kept []error
		for _, child := range joined.Unwrap() {
			kept = append(kept, unlessCancellation(child, cause))
		}
		return errors.Join(kept...)
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if child := wrapped.Unwrap(); child != nil && unlessCancellation(child, cause) == nil {
			return nil
		}
		return err
	}
	if errors.Is(err, context.Canceled) || (cause != nil && errors.Is(err, cause)) {
		return nil
	}
	return err
}
