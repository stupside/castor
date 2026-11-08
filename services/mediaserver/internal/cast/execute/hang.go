package execute

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

var (
	// hangWindow is how long an encoder with input waiting may make nothing before castor counts it hung.
	hangWindow = 15 * time.Second
	// chainHangWindow is that wait in a chain, which keeps the device only lead ahead.
	chainHangWindow = 5 * time.Second
)

const (
	// hangMargin is the input an encoder must have left unread for its silence to be its own: one that waits for input has read to the end.
	hangMargin = 64 << 10
	// hangPoll is how often a silent encoder is looked at.
	hangPoll = 250 * time.Millisecond
)

// encoderHung is an encoder castor stopped for making nothing while its input waited.
type encoderHung struct{ waited time.Duration }

func (e *encoderHung) Error() string {
	return fmt.Sprintf("the encoder made nothing for %s while its input waited", e.waited.Round(time.Second))
}

// waitClock times a read of an encoder's output that is waiting for it.
type waitClock struct{ since atomic.Int64 }

func (w *waitClock) start() { w.since.Store(time.Now().UnixNano()) }
func (w *waitClock) stop()  { w.since.Store(0) }

// waited is how long the read in flight has waited, zero with none in flight.
func (w *waitClock) waited() time.Duration {
	if since := w.since.Load(); since != 0 {
		return time.Since(time.Unix(0, since))
	}
	return 0
}

// timedOutput is an encoder's output whose reads are timed.
type timedOutput struct {
	io.Reader
	clock *waitClock
}

func (t timedOutput) Read(p []byte) (int, error) {
	t.clock.start()
	defer t.clock.stop()
	return t.Reader.Read(p)
}

// countedInput is an encoder's input that counts what the encoder took of it.
type countedInput struct {
	io.ReadCloser
	took atomic.Int64
}

func (c *countedInput) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.took.Add(int64(n))
	return n, err
}

// silent is how long castor has waited on an encoder's output while the encoder left input unread, zero otherwise.
func silent(waited time.Duration, unread int64) time.Duration {
	if unread < hangMargin {
		return 0
	}
	return waited
}

// watchHang calls hung once quiet has reached window, looking until ctx ends.
func watchHang(ctx context.Context, window time.Duration, quiet func() time.Duration, hung func(waited time.Duration)) {
	tick := time.NewTicker(hangPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if waited := quiet(); waited >= window {
				hung(waited)
				return
			}
		}
	}
}
