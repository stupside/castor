package encoding

import (
	"context"
	"fmt"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// ValidateOutput checks a growing encoded artifact against receiver capabilities.
// It observes production completion without owning or cancelling the producer.
func ValidateOutput(ctx context.Context, prober media.Prober, caps media.Capabilities, contentType string, expected media.TrackRequirements, done <-chan struct{}) error {
	return validateTracks(ctx, prober, expected, done, func(measured media.ProbeInfo) error {
		if !caps.CanPlayIn(contentType, measured) {
			return fmt.Errorf("encoded output does not satisfy receiver capabilities: video=%s profile=%s level=%d audio=%s", measured.VideoCodec, measured.VideoProfile, measured.VideoLevel, measured.AudioCodec)
		}
		return nil
	})
}

// ValidateOriginalOutput checks a native trial against its source, not unverified
// receiver defaults. Missing tracks and incomplete codec headers still fail readiness.
func ValidateOriginalOutput(ctx context.Context, prober media.Prober, original media.ProbeInfo, expected media.TrackRequirements, done <-chan struct{}) error {
	return validateTracks(ctx, prober, expected, done, func(measured media.ProbeInfo) error {
		for _, check := range []struct {
			field     string
			unchanged bool
		}{
			{"video codec", original.VideoCodec == "" || original.VideoCodec == measured.VideoCodec},
			{"video profile", original.VideoProfile == "" || original.VideoProfile == measured.VideoProfile},
			{"video level", original.VideoLevel <= 0 || original.VideoLevel == measured.VideoLevel},
			{"video bit depth", original.VideoBitDepth == 0 || original.VideoBitDepth == measured.VideoBitDepth},
			{"video height", original.VideoHeight == 0 || original.VideoHeight == measured.VideoHeight},
			{"HDR", !original.VideoHDR || measured.VideoHDR},
			{"audio codec", original.AudioCodec == "" || original.AudioCodec == measured.AudioCodec},
			{"audio channels", original.AudioChannels == 0 || original.AudioChannels == measured.AudioChannels},
			{"audio sample rate", original.AudioSampleRate == 0 || original.AudioSampleRate == measured.AudioSampleRate},
		} {
			if !check.unchanged {
				return fmt.Errorf("native output changed the source's %s", check.field)
			}
		}
		return nil
	})
}

// ValidateTracks checks that a growing read still carries the tracks measured at its source.
func ValidateTracks(ctx context.Context, prober media.Prober, expected media.TrackRequirements, done <-chan struct{}) error {
	return validateTracks(ctx, prober, expected, done, nil)
}

func validateTracks(ctx context.Context, prober media.Prober, expected media.TrackRequirements, done <-chan struct{}, accept func(media.ProbeInfo) error) error {
	completed := false
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("validating encoded output: %w", err)
		}
		measured, _, err := prober.Probe(ctx)
		if cause := ctx.Err(); cause != nil {
			return fmt.Errorf("validating encoded output: %w", cause)
		}
		if err == nil && (!expected.Video || measured.VideoCodec != "") && (!expected.Audio || measured.AudioCodec != "") {
			// Container headers can identify tracks and dimensions before the first
			// video packet supplies pixel format / codec parameters. A successful
			// ffprobe exit alone does not make that growing artifact complete.
			incomplete := measured.HasVideo() && (measured.VideoCodec == "" || measured.VideoBitDepth == 0 ||
				((measured.VideoCodec == media.CodecH264 || measured.VideoCodec == media.CodecHEVC) && (measured.VideoProfile == "" || measured.VideoLevel < 0))) ||
				measured.HasAudio() && measured.AudioCodec == ""
			switch {
			case incomplete:
				err = fmt.Errorf("encoded artifact does not yet contain codec parameters")
			default:
				if accept != nil {
					return accept(measured)
				}
				return nil
			}
		}
		if err == nil {
			err = fmt.Errorf("encoded artifact does not yet contain all expected tracks")
		}
		if completed {
			return fmt.Errorf("validating completed encoded output: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("validating encoded output: %w", ctx.Err())
		case <-done:
			// The probe may have raced the producer's last write. Observe the final
			// artifact once before deciding it is permanently incomplete.
			completed = true
			done = nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}
