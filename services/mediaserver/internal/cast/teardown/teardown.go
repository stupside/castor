// Package teardown tells the errors stopping work provokes from failures that coincide with the stop.
//
// Whoever stops work drops what the stop provoked once, before wrapping or
// recording the result: a wrapper is kept whole unless all it wraps was provoked.
package teardown

import (
	"context"
	"errors"
	"io"
	"os"
)

// Failures is err without what stopping ctx provoked: its cancellation, its
// cause, and I/O closed under it. While ctx runs, nothing was provoked.
func Failures(ctx context.Context, err error) error {
	if err == nil || ctx.Err() == nil {
		return err
	}
	kept, _ := without(err, func(leaf error) bool {
		return errors.Is(leaf, ctx.Err()) || errors.Is(leaf, context.Cause(ctx)) ||
			errors.Is(leaf, os.ErrClosed) || errors.Is(leaf, io.ErrClosedPipe)
	})
	return kept
}

// without drops the provoked leaves of err, and reports whether it dropped any.
func without(err error, provoked func(error) bool) (error, bool) {
	switch e := err.(type) {
	case interface{ Unwrap() []error }:
		var kept []error
		dropped := false
		for _, child := range e.Unwrap() {
			k, d := without(child, provoked)
			kept, dropped = append(kept, k), dropped || d
		}
		if !dropped {
			return err, false
		}
		return errors.Join(kept...), true
	case interface{ Unwrap() error }:
		if child := e.Unwrap(); child != nil {
			if k, d := without(child, provoked); d && k == nil {
				return nil, true
			}
			return err, false
		}
	}
	if provoked(err) {
		return nil, true
	}
	return err, false
}
