package whisper

import (
	"context"
	"errors"
	"testing"
)

func TestCanceledInferenceDoesNotAcquireNativeContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// A nil model would panic if native context creation ran after cancellation.
	_, err := (recognizer{}).Recognize(ctx, nil, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled recognition=%v", err)
	}
}
