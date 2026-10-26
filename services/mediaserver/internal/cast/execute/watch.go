package execute

import (
	"context"
	"fmt"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
)

// gate holds a buffered cast until its read has proven it can deliver, and its transcription leads.
func gate(ctx context.Context, buf *buffered) error {
	return awaitHealth(ctx, readMonitor(buf.reader, health.Monitor{
		Subject: "playback gate",
		Phase:   health.Reading,
		Lead:    buf.transcript,
	}))
}

// awaitArtifact waits for the artifact the device will fetch to exist.
func awaitArtifact(ctx context.Context, d delivery) error {
	artifact := d.sink.Artifact()
	return awaitHealth(ctx, health.Monitor{
		Subject:   artifact.Subject,
		Phase:     health.Opening,
		Producer:  d.output,
		Telemetry: d.output,
		Landed:    artifact.Landed,
		Grace:     artifact.Grace,
	})
}

// readMonitor judges a buffered cast by its read.
func readMonitor(reader *pull, m health.Monitor) health.Monitor {
	m.Producer = reader
	m.Telemetry = reader
	m.Landed = func() (int64, error) { return reader.spool.Size(), nil }
	return m
}

// playingMonitor judges a buffered cast by its read, and one reading its source by the encoder serving it.
func playingMonitor(f feed, d delivery, aud health.Audience, dev Device) health.Monitor {
	m := health.Monitor{Subject: "the playing cast", Phase: health.Playing, Audience: aud}
	if p, ok := dev.(health.Playhead); ok {
		m.Playhead = p
	}
	if f.buffered != nil {
		// Only a read that can get ahead of the device is worth holding the device for.
		if !f.buffered.live {
			m.Holdable = true
		}
		return readMonitor(f.buffered.reader, m)
	}
	m.Producer, m.Telemetry = d.output, d.output
	m.Landed = d.sink.Artifact().Landed
	return m
}

// awaitHealth observes preparation gates, which cannot request playback control.
func awaitHealth(ctx context.Context, monitor health.Monitor) error {
	decision, err := health.Observe(monitor).Next(ctx)
	if err != nil {
		return err
	}
	if decision != health.Complete {
		return fmt.Errorf("preparation requested playback control: %d", decision)
	}
	return nil
}

// playbackControl is the command port borrowed by the health worker.
// The attempt cancels and joins that worker before releasing the device.
type playbackControl interface {
	Pause(context.Context) error
	Resume(context.Context) error
}

// observeHealth applies each decision synchronously in the same worker that
// reports it to the observer. The health FSM itself has no device dependency.
func observeHealth(ctx context.Context, m health.Monitor, device playbackControl) error {
	observer := health.Observe(m)
	for {
		decision, err := observer.Next(ctx)
		if err != nil || decision == health.Complete {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		switch decision {
		case health.Pause:
			err = device.Pause(ctx)
		case health.Resume:
			err = device.Resume(ctx)
		default:
			return fmt.Errorf("unknown playback control decision: %d", decision)
		}
		if err := observer.Applied(ctx, decision, err); err != nil {
			return err
		}
	}
}
