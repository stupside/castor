// Package teardown drops the errors stopping work provokes, keeping failures joined to them.
package teardown

import (
	"context"
	"errors"
	"io"
	"os"
)

// Failures is err without its cancellation, cause and I/O closed by stopping ctx; a running ctx provoked nothing.
func Failures(ctx context.Context, err error) error {
	if ctx.Err() == nil {
		return err
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var kept []error
		for _, child := range joined.Unwrap() {
			kept = append(kept, Failures(ctx, child))
		}
		return errors.Join(kept...)
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if child := wrapped.Unwrap(); child != nil && Failures(ctx, child) == nil {
			return nil
		}
		return err
	}
	if errors.Is(err, ctx.Err()) || errors.Is(err, context.Cause(ctx)) || errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}
