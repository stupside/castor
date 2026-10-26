package health

import (
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Monitor is one subject under watch.
type Monitor struct {
	// Subject is what's being waited on (e.g. "playback gate", "HLS playlist").
	Subject string

	// Phase is how far the cast has got, which decides what each verdict asks for.
	Phase Phase

	// Producer is the source read or encoder; a nil port is unmeasured, not absent.
	Producer Producer

	// Telemetry is the producer's pace (nil where rate is not judged).
	Telemetry Telemetry

	// Audience is the device side (nil until device holds URL).
	Audience Audience

	// Served maps the bytes the device took to media (nil where unmeasured).
	Served Telemetry

	Playhead Playhead

	// Holdable says whether the attempt can pause this playback.
	Holdable bool

	// Switchable is a delivery whose rate can change mid-play.
	Switchable bool

	// MaxBitrate bounds the rate asked of the device, zero for none.
	MaxBitrate media.Bitrate

	// Lead is the transcription frontier (nil if no burn-in).
	Lead Lead

	// Landed is the artifact's own size, so the gate opens on the artifact rather than the producer.
	Landed func() (int64, error)

	// Grace is how long the artifact may take; zero waits on it alone.
	Grace time.Duration
}

// Producer is what makes the watched bytes: when it ended, and what it printed.
type Producer interface {
	Done() <-chan struct{}
	Evidence() []string
}

// Telemetry is a producer's media pace, judged in media rather than bytes a muxer pads.
type Telemetry interface {
	Progress() media.Progress
	Err() error
}

// Lead is how far a transcription has committed, in seconds of media, and whether it has finished.
type Lead interface {
	LatestEnd() float64
	Done() bool
}

// Audience is what the device took, to tell a pause from a device gone.
type Audience interface {
	// Handed is the bytes taken and when the last one moved.
	Handed() (bytes int64, last time.Time)
}
