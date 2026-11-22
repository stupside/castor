package execute

import (
	"context"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
)

// withPlace asks the device where it got to when its encode failed in a delivery no watch placed the viewer in.
func withPlace(ctx context.Context, dev Device, a recovery.Attempt, e recovery.Evidence) recovery.Evidence {
	// A live program resumes at its edge, and a gone device has no place to say.
	if ctx.Err() != nil || e.EncodeErr == nil || e.Reached < health.Playing || e.DeviceGone != nil || a.Live() {
		return e
	}
	if _, placed := e.Vitals.Reached(); placed {
		return e
	}
	// A device that has played says more than nothing; one that says nothing has not been watched.
	if at, _, known := askPosition(ctx, dev); known && at > 0 {
		e.Vitals = e.Vitals.WithHead(at)
	}
	return e
}

// askPosition is where dev says it plays, within askFor; known is false for a device with no playhead.
func askPosition(ctx context.Context, dev Device) (at time.Duration, held, known bool) {
	head, ok := dev.(health.Playhead)
	if !ok {
		return 0, false, false
	}
	asking, cancel := context.WithTimeout(ctx, askFor)
	defer cancel()
	return head.Position(asking)
}
