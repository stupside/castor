package execute

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
)

func TestCustomCancellationCauseDoesNotBecomeACleanupFailure(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(fmt.Sprint(cleanupFails), func(t *testing.T) {
			stopped := errors.New("cast stopped")
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(stopped)
			c := newCoordinator(ctx, Cast{}, recovery.Attempt{})
			var cleanup error
			if cleanupFails {
				cleanup = errors.New("storage close failed")
			}
			c.releases.worker(nil, func() error {
				return errors.Join(fmt.Errorf("worker: %w", context.Cause(c.ctx)), cleanup)
			})
			_, err := c.run()
			interrupted, ok := errors.AsType[*recovery.Interrupted](err)
			if !ok || !errors.Is(interrupted.Cause, stopped) {
				t.Fatalf("lost cancellation cause: %v", err)
			}
			if cleanup == nil && interrupted.Failure != nil {
				t.Fatalf("clean stop became a failure: %v", interrupted.Failure)
			}
			if cleanup != nil && !errors.Is(interrupted.Failure, cleanup) {
				t.Fatalf("lost real cleanup failure: %v", interrupted.Failure)
			}
		})
	}
}
