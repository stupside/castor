package cast

import (
	"testing"

	"connectrpc.com/connect"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/admission"
	"golang.org/x/sync/semaphore"
)

func TestWatchRejectsAtCapacityBeforeFindingCast(t *testing.T) {
	s := &Service{ctx: t.Context(), watchers: semaphore.NewWeighted(256), resolutions: admission.New(t.Context(), 16)}
	for range 256 {
		if !s.watchers.TryAcquire(1) {
			t.Fatal("cannot occupy capacity")
		}
		defer s.watchers.Release(1)
	}
	err := s.Watch(t.Context(), &castorv1.WatchRequest{CastId: "missing"}, nil)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("capacity = %v", err)
	}
}

func TestResolveRejectsAtCapacity(t *testing.T) {
	s := &Service{ctx: t.Context(), watchers: semaphore.NewWeighted(256), resolutions: admission.New(t.Context(), 16)}
	for range 16 {
		release, err := s.resolutions.Take()
		if err != nil {
			t.Fatal("cannot occupy capacity")
		}
		defer release()
	}
	_, err := s.Resolve(t.Context(), &castorv1.ResolveRequest{})
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("capacity = %v", err)
	}
}
