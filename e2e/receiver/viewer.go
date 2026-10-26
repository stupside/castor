package receiver

import (
	"context"
	"io"
	"time"

	"github.com/stupside/castor/e2e/strategy"
)

// Viewer is how the person in front of the receiver watches: to the end, or until they stop it.
type Viewer interface {
	strategy.Named
	// Watch bounds playback; the returned context ends when the viewer stops.
	Watch(ctx context.Context) (context.Context, context.CancelFunc)
	// Stopped reports whether a playback watched with ctx ended at the viewer's hand.
	Stopped(ctx context.Context) bool
	// WillStop reports whether this viewer means to stop before the media ends.
	WillStop() bool
	// Pace wraps a stream read the way this viewer consumes it.
	Pace(r io.Reader) io.Reader
	// Realtime reports whether the viewer consumes at playback speed, which a demuxer must be told.
	Realtime() bool
}

// Jumper is a viewer who, after a while into playback, jumps to another position.
type Jumper interface {
	Viewer
	Seeks() (after, to time.Duration)
}
