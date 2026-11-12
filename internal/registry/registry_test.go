package registry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestAValueLingersAfterItIsDoneThenIsForgotten(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := New[int](time.Minute)
		defer r.Drain(context.Background())
		done := make(chan struct{})
		if err := r.Add("a", 1, done); err != nil {
			t.Fatalf("an open registry refused a value: %v", err)
		}
		close(done)
		synctest.Sleep(59 * time.Second)
		if _, ok := r.Find("a"); !ok {
			t.Fatal("a done value was forgotten before it lingered")
		}
		synctest.Sleep(2 * time.Second)
		if _, ok := r.Find("a"); ok {
			t.Fatal("a done value was kept past its linger")
		}
	})
}

func TestADrainingRegistryRefusesValuesAndWaitsForThoseRunning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := New[int](time.Minute)
		defer r.Drain(context.Background())
		done := make(chan struct{})
		r.Add("a", 1, done)
		drained := make(chan struct{})
		go func() {
			r.Drain(t.Context())
			close(drained)
		}()
		synctest.Wait()
		if err := r.Add("b", 2, make(chan struct{})); !errors.Is(err, ErrDraining) {
			t.Fatalf("a draining registry took a value: %v", err)
		}
		select {
		case <-drained:
			t.Fatal("the drain returned while a value still ran")
		default:
		}
		close(done)
		synctest.Wait()
		select {
		case <-drained:
		default:
			t.Fatal("the drain did not return once every value was done")
		}
	})
}

func TestAnIDInUseIsRefusedAndTheFirstValueKeepsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := New[int](time.Minute)
		defer r.Drain(context.Background())
		first := make(chan struct{})
		if err := r.Add("a", 1, first); err != nil {
			t.Fatal(err)
		}
		if err := r.Add("a", 2, make(chan struct{})); !errors.Is(err, ErrTaken) {
			t.Fatalf("a second value under a taken id: %v, want it refused", err)
		}
		close(first)
		synctest.Sleep(2 * time.Minute)
		if _, ok := r.Find("a"); ok {
			t.Fatal("the id was never released")
		}
		again := make(chan struct{})
		if err := r.Add("a", 3, again); err != nil {
			t.Fatalf("an id released after its linger: %v", err)
		}
		close(again)
	})
}

func TestCapacityBoundsRunningAndRetainedWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newWithLimits[int](time.Minute, limits{Active: 1, Retained: 2})
		defer r.Drain(context.Background())
		first := make(chan struct{})
		if err := r.Add("a", 1, first); err != nil {
			t.Fatal(err)
		}
		if err := r.Add("b", 2, make(chan struct{})); !errors.Is(err, ErrCapacity) {
			t.Fatalf("active capacity = %v", err)
		}
		close(first)
		synctest.Wait()
		second := make(chan struct{})
		if err := r.Add("b", 2, second); err != nil {
			t.Fatal(err)
		}
		close(second)
		synctest.Wait()
		if err := r.Add("c", 3, make(chan struct{})); !errors.Is(err, ErrCapacity) {
			t.Fatalf("retained capacity = %v", err)
		}
		synctest.Sleep(2 * time.Minute)
		done := make(chan struct{})
		close(done)
		if err := r.Add("c", 3, done); err != nil {
			t.Fatalf("expired capacity not released: %v", err)
		}
	})
}

func TestCanceledDrainCanBeRetriedWithoutReopeningAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := New[int](time.Minute)
		defer r.Drain(context.Background())
		done := make(chan struct{})
		if err := r.Add("running", 1, done); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		r.Drain(ctx)
		if err := r.Add("late", 2, done); !errors.Is(err, ErrDraining) {
			t.Fatal(err)
		}
		var drains sync.WaitGroup
		for range 3 {
			drains.Go(func() { r.Drain(t.Context()) })
		}
		synctest.Wait()
		close(done)
		drains.Wait()
		r.Drain(t.Context())
	})
}

func TestDrainEndsRetentionWorkersWithoutWaitingForLinger(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := New[int](24 * time.Hour)
		defer r.Drain(context.Background())
		done := make(chan struct{})
		if err := r.Add("finished", 1, done); err != nil {
			t.Fatal(err)
		}
		close(done)
		synctest.Wait()
		before := time.Now()
		r.Drain(t.Context())
		if time.Since(before) != 0 {
			t.Fatal("drain waited for retention timer")
		}
		if value, ok := r.Find("finished"); !ok || value != 1 {
			t.Fatal("shutdown hid completed work from remaining watchers")
		}
	})
}

func TestADrainingRegistryFullOfLingeringValuesSaysItIsDraining(t *testing.T) {
	r := newWithLimits[int](time.Minute, limits{Active: 1, Retained: 1})
	done := make(chan struct{})
	close(done)
	if err := r.Add("first", 1, done); err != nil {
		t.Fatal(err)
	}
	r.Drain(t.Context())
	if err := r.Add("second", 2, done); !errors.Is(err, ErrDraining) {
		t.Errorf("a value offered while draining with retention full: %v, want ErrDraining", err)
	}
}
