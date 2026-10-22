package execute

import (
	"context"
	"errors"
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/cast/teardown"
)

// Preparation registers releases; the coordinator takes ownership after joining it.
// All workers and publications are joined before any storage is released.
// Passive resources are then released in reverse acquisition order.
type releases struct {
	// ctx is what stopping the workers cancels; their joins drop what that provoked.
	ctx     context.Context
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
		errs = append(errs, teardown.Failures(r.ctx, join()))
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
			err = errors.Join(err, teardown.Failures(c.ctx, op.err))
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
		err = &recovery.Interrupted{Cause: c.interruption, Failure: err}
	}
	return p.reached, err
}
