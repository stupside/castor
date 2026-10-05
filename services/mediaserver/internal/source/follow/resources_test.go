package follow

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestACancelledResourceCallerDoesNotWaitForTheSharedRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cache resources[string]
		started, release := make(chan struct{}), make(chan struct{})
		ctx, cancel := context.WithCancel(t.Context())
		finished := make(chan error, 1)
		go func() {
			_, err := cache.get(ctx, "key", func(context.Context) ([]byte, error) {
				close(started)
				<-release
				return []byte("secret"), nil
			})
			finished <- err
		}()
		<-started
		cancel()
		synctest.Wait()
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("get = %v, want cancellation", err)
			}
		default:
			t.Error("the cancelled caller is still waiting on the shared read")
		}
		close(release)
		synctest.Wait()
		b, err := cache.get(t.Context(), "key", func(context.Context) ([]byte, error) {
			t.Error("the successful shared read was not cached")
			return nil, errors.New("unexpected second read")
		})
		if err != nil || string(b) != "secret" {
			t.Errorf("cached key = %q (%v)", b, err)
		}
	})
}
