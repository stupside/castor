package execute

import (
	"errors"
	"slices"
	"testing"
)

// Releasing runs last acquired first, and reports every failure.
func TestReleasesRunLastAcquiredFirstAndReportEveryFailure(t *testing.T) {
	var order []string
	first, last := errors.New("first failed"), errors.New("last failed")
	var r releases
	r.push(func() error { order = append(order, "workspace"); return first })
	r.push(func() error {
		order = append(order, "read")
		return nil
	})
	r.push(func() error { order = append(order, "encoder"); return last })

	err := r.release()
	if want := []string{"encoder", "read", "workspace"}; !slices.Equal(order, want) {
		t.Errorf("released %v, want %v", order, want)
	}
	if !errors.Is(err, first) || !errors.Is(err, last) {
		t.Errorf("release = %v, want both failures", err)
	}
	if err := r.release(); err != nil || len(order) != 3 {
		t.Errorf("a second release ran something again (%v, %v)", err, order)
	}
}

func TestScopeJoinsAllUsersBeforeReleasingAnyStorage(t *testing.T) {
	r := releases{ctx: t.Context()}
	workerFailure := errors.New("worker failed")
	storageFailure := errors.New("storage close failed")
	stopped, joined, released := 0, 0, 0
	for range 2 {
		r.worker(func() { stopped++ }, func() error {
			if stopped != 2 {
				t.Errorf("joined before all stop requests: %d", stopped)
			}
			if released != 0 {
				t.Error("storage released while a worker still owns it")
			}
			joined++
			return workerFailure
		})
		// A resource acquired later must still outlive every worker, regardless
		// of where its release falls in the acquisition order.
		r.push(func() error {
			if joined != 2 {
				t.Errorf("released storage with %d workers joined", joined)
			}
			released++
			return storageFailure
		})
	}
	// The coordinator signals before joining its own operations.
	r.signal()
	err := r.release()
	if !errors.Is(err, workerFailure) || !errors.Is(err, storageFailure) {
		t.Fatalf("lost shutdown failure: %v", err)
	}
	if err := r.release(); err != nil || stopped != 2 || joined != 2 || released != 2 {
		t.Fatalf("shutdown repeated: stops=%d joins=%d releases=%d err=%v", stopped, joined, released, err)
	}
}
