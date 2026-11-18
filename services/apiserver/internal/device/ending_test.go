package device

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestAnEndingIsDecidedByItsFirstFinish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := NewEnding()
		first := errors.New("refused")
		e.Finish(first)
		e.Finish(nil)
		if err := AwaitEnding(t.Context(), e, make(chan struct{}), "Living Room", "closed"); !errors.Is(err, first) {
			t.Fatalf("AwaitEnding = %v, want the first end", err)
		}
	})
}

func TestAConnectionThatClosesNamesTheDeviceGoneUnlessTheEndWasReadFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gone := make(chan struct{})
		close(gone)
		if _, isGone := errors.AsType[*Gone](AwaitEnding(t.Context(), NewEnding(), gone, "Living Room", "the connection closed")); !isGone {
			t.Error("a closed connection with no end read is not the device gone")
		}
		read := NewEnding()
		read.Finish(nil)
		if err := AwaitEnding(t.Context(), read, gone, "Living Room", "the connection closed"); err != nil {
			t.Errorf("AwaitEnding = %v, want the end that was read before the connection closed", err)
		}
	})
}

func TestAPlaybackNeverBegunNeverEndsButWithItsConnectionOrItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(errors.New("stopped"))
		if err := AwaitEnding(ctx, nil, make(chan struct{}), "Living Room", "closed"); err == nil || err.Error() != "stopped" {
			t.Fatalf("AwaitEnding = %v, want the context's cause", err)
		}
	})
}
