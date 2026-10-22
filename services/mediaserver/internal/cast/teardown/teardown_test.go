package teardown_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/teardown"
)

func TestFailuresKeepOnlyWhatTheStopDidNotProvoke(t *testing.T) {
	stopped := errors.New("cast stopped")
	failure := errors.New("disk full")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(stopped)
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"cancellation", fmt.Errorf("worker: %w", context.Canceled), nil},
		{"custom cause", fmt.Errorf("worker: %w", stopped), nil},
		{"closed file", &os.PathError{Op: "read", Path: "|0", Err: os.ErrClosed}, nil},
		{"closed pipe", io.ErrClosedPipe, nil},
		{"independent failure", failure, failure},
		{"failure joined to the stop", errors.Join(stopped, os.ErrClosed, failure), failure},
		// A deadline is provoked only when it is the context's own.
		{"unrelated deadline", fmt.Errorf("fetch: %w", context.DeadlineExceeded), context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := teardown.Failures(ctx, tc.err)
			if tc.want == nil && got != nil {
				t.Fatalf("Failures = %v, want nil", got)
			}
			if tc.want != nil && !errors.Is(got, tc.want) {
				t.Fatalf("Failures = %v, want %v kept", got, tc.want)
			}
			if errors.Is(got, stopped) || errors.Is(got, os.ErrClosed) {
				t.Fatalf("Failures = %v, kept what the stop provoked", got)
			}
		})
	}
}

func TestNothingIsProvokedWhileTheContextRuns(t *testing.T) {
	err := errors.Join(context.Canceled, os.ErrClosed)
	if got := teardown.Failures(t.Context(), err); got != err {
		t.Fatalf("Failures = %v, want %v whole", got, err)
	}
}

func TestATimedOutContextProvokedItsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 0)
	defer cancel()
	<-ctx.Done()
	if got := teardown.Failures(ctx, fmt.Errorf("probe: %w", context.DeadlineExceeded)); got != nil {
		t.Fatalf("Failures = %v, want nil", got)
	}
}
