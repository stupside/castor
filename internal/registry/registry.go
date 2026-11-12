// Package registry keeps what a server runs by id, findable for a while after it ends, until the server drains.
package registry

import (
	"context"
	"errors"
	"iter"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/stupside/castor/internal/admission"
)

var (
	// ErrDraining refuses a value once the registry takes no more.
	ErrDraining = errors.New("the registry is draining")
	// ErrTaken refuses a value under an id another still holds, or lingers under.
	ErrTaken    = errors.New("the id is taken")
	ErrCapacity = errors.New("the registry capacity is exhausted")
)

// Registry holds values by id, each findable until linger after it is done.
type Registry[V any] struct {
	linger time.Duration
	limits limits
	// active lets values in while the registry lasts, as many running as limits.Active.
	active *admission.Gate

	mu       sync.Mutex
	byID     map[string]V
	lifetime context.Context
	stop     context.CancelFunc
	workers  sync.WaitGroup
}

// limits bounds running work and retained terminal records independently.
type limits struct{ Active, Retained int }

const defaultActiveLimit = 128
const defaultRetainedLimit = 1024

// New is an empty Registry whose values linger after they are done.
func New[V any](linger time.Duration) *Registry[V] {
	return newWithLimits[V](linger, limits{Active: defaultActiveLimit, Retained: defaultRetainedLimit})
}

// newWithLimits constructs a registry with explicit admission budgets.
func newWithLimits[V any](linger time.Duration, limits limits) *Registry[V] {
	if limits.Active <= 0 || limits.Retained < limits.Active {
		panic("invalid registry capacity limits")
	}
	lifetime, stop := context.WithCancel(context.Background())
	return &Registry[V]{linger: linger, limits: limits, active: admission.New(lifetime, int64(limits.Active)), byID: map[string]V{}, lifetime: lifetime, stop: stop}
}

// Add keeps v under id until linger after done closes, unless the registry is draining or the id is taken.
func (r *Registry[V]) Add(id string, v V, done <-chan struct{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	release, err := r.active.Take()
	switch {
	case errors.Is(err, admission.ErrFull):
		return ErrCapacity
	case err != nil:
		return ErrDraining
	}
	if _, taken := r.byID[id]; taken {
		release()
		return ErrTaken
	}
	if len(r.byID) >= r.limits.Retained {
		release()
		return ErrCapacity
	}
	r.byID[id] = v
	r.workers.Go(func() {
		<-done
		release()
		timer := time.NewTimer(r.linger)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.lifetime.Done():
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		// Shutdown stops retention work, but watchers still need the final
		// snapshots while the server drains its HTTP streams.
		if r.lifetime.Err() == nil {
			delete(r.byID, id)
		}
	})
	return nil
}

// Find is the value kept under id.
func (r *Registry[V]) Find(id string) (V, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.byID[id]
	return v, ok
}

// All is every value kept now.
func (r *Registry[V]) All() iter.Seq[V] {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Values(slices.Collect(maps.Values(r.byID)))
}

// Drain takes no more values and waits until every one is done, or ctx ends.
func (r *Registry[V]) Drain(ctx context.Context) {
	r.mu.Lock()
	r.stop()
	r.mu.Unlock()
	// A canceled drain leaves no extra goroutine waiting for work that may never finish.
	if r.active.Wait(ctx) == nil {
		r.workers.Wait()
	}
}
