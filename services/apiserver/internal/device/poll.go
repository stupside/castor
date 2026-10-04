package device

import (
	"context"
	"fmt"
	"time"
)

const (
	// Cadence and budget are separate: a device busy buffering is slow, not absent.
	PollInterval = 2 * time.Second
	PollTimeout  = 10 * time.Second
)

// UnreachableWindow is how long a device may go unanswered before it is gone, whatever each poll costs.
const UnreachableWindow = 150 * time.Second

// AwaitPolledEnd asks the device question on every interval, each within PollTimeout, until poll says it is over, it goes unanswered for the window, or ctx ends.
func AwaitPolledEnd(ctx context.Context, name, question string, poll func(context.Context) (bool, error)) error {
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	answered := time.Now()
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticker.C:
		}
		asking, cancel := context.WithTimeout(ctx, PollTimeout)
		over, err := poll(asking)
		cancel()
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if err != nil {
			if silent := time.Since(answered); silent >= UnreachableWindow {
				// Last failure explains how (refused, timed out, no route).
				return &Gone{
					Device:   name,
					Observed: fmt.Sprintf("%s went unanswered for %s", question, silent.Round(time.Second)),
					Err:      err,
				}
			}
			continue
		}
		answered = time.Now()
		if over {
			return nil
		}
	}
}
