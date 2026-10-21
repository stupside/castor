package output

import (
	"context"
	"errors"
	"io"
	"os"
)

// InterruptedIO removes expected I/O shutdown errors after cancellation.
// Callers must have observed cancellation; independent joined failures remain.
func InterruptedIO(err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var kept []error
		for _, child := range joined.Unwrap() {
			kept = append(kept, InterruptedIO(child))
		}
		return errors.Join(kept...)
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if child := wrapped.Unwrap(); child != nil && InterruptedIO(child) == nil {
			return nil
		}
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
