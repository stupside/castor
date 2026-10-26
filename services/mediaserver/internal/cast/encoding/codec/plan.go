// Package codec decides, track by track, what a cast copies and what it encodes, and into which container.
package codec

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Encoders finds the host's working encoder for a codec.
type Encoders func(context.Context, media.Codec) (ffmpeg.Encoder, bool)

// Inputs is everything a plan is decided against.
type Inputs struct {
	// Caps is the connected device's advertised support.
	Caps     media.Capabilities
	Probe    media.ProbeInfo
	Measured bool
	// Into is the container this encode writes.
	Into      container.Format
	Decode    media.Axes
	MaxHeight media.HeightCap
	// MaxBitrate bounds what the device is asked to take; Rate is what a copy weighs, 0 where unmeasured.
	MaxBitrate media.Bitrate
	Rate       media.Bitrate
	// KeepAudio is the audio codec a running delivery carries, which a device cannot switch away from mid-stream.
	KeepAudio media.Codec
	// Spliced is a source stitched from pieces encoded apart, or live and free to become so, which only a re-encode hands over as one stream.
	Spliced bool
	BurnIn  string
	// Encoders is the host's encoder lookup; required.
	Encoders Encoders
}

// Plan is the complete codec plan for an FFmpeg encode; Refusals explain why it is not a copy.
type Plan struct {
	Video    Track[VideoEncode]
	Audio    Track[AudioEncode]
	Refusals []Reason
}

// DeviceRefusedVideo reports that the device's own envelope, not the container or the request, ruled out copying the video.
func (p Plan) DeviceRefusedVideo() bool { return slices.Contains(p.Refusals, reasonDeviceVideo) }

// Encoded returns which halves of the program this plan produces rather than passes through.
func (p Plan) Encoded() media.Axes {
	_, video := p.Video.Encode()
	_, audio := p.Audio.Encode()
	return media.Axes{Video: video, Audio: audio}
}

// Copied excludes omitted tracks: recovery can only revise a copy that ran.
func (p Plan) Copied() media.Axes {
	return media.Axes{Video: p.Video.Action() == CopyTrack, Audio: p.Audio.Action() == CopyTrack}
}

// Served plans the encode a device is served, from its capabilities, track by track.
func Served(ctx context.Context, in Inputs) (Plan, error) {
	if len(in.Caps.Formats) > 0 {
		var best Plan
		var bestStanding standing
		found := false
		var last error
		for _, f := range in.Caps.Formats {
			if f.Transport.ContentType() != in.Into.ContentType || (in.KeepAudio != "" && f.Audio.Codec != in.KeepAudio) {
				continue
			}
			candidate := in
			candidate.Caps.Formats = []media.FormatSupport{f}
			plan, err := servedTracks(ctx, candidate)
			if err != nil {
				last = err
				continue
			}
			if s := standingOf(in, plan); !found || s.before(bestStanding) {
				best, bestStanding, found = plan, s, true
			}
		}
		switch {
		case found:
			return best, nil
		case last != nil:
			return Plan{}, fmt.Errorf("no usable receiver combination for %s: %w", in.Into.ContentType, last)
		case in.KeepAudio != "":
			return Plan{}, fmt.Errorf("no receiver format for %s carries the kept %s audio", in.Into.ContentType, in.KeepAudio)
		}
		return Plan{}, fmt.Errorf("no receiver format for %s", in.Into.ContentType)
	}

	return Plan{}, fmt.Errorf("no playback envelopes advertised")
}

// servedTracks plans the sound first, so the picture is proven and given its budget against the cast that sound makes.
func servedTracks(ctx context.Context, in Inputs) (Plan, error) {
	audio, audioRefusals, audioErr := decideAudio(in)
	// An encoded sound replaces the source's, of unknown weight where nobody weighed it.
	if enc, ok := audio.Encode(); ok && in.Rate > 0 {
		in.Rate += max(0, enc.Bitrate-in.Probe.AudioBitrate)
	}
	video, videoRefusals, err := decideVideo(ctx, in, weighs(in, audio))
	if err != nil {
		return Plan{}, fmt.Errorf("planning video: %w", err)
	}
	if audioErr != nil {
		return Plan{}, fmt.Errorf("planning audio: %w", audioErr)
	}
	return Plan{Video: video, Audio: audio, Refusals: append(videoRefusals, audioRefusals...)}, nil
}

// standing ranks one device format's plan against another's; lower comes first, field by field.
type standing struct {
	// floored is a plan with bitrate-floor, which loses to any that keeps under its ceiling.
	floored int
	// cost counts a video encode above an audio one, so a tie never re-encodes the picture to spare the sound.
	cost int
	// squeezed is a picture the sound takes budget from; only then does the ceiling weigh one sound against another.
	squeezed int
	// heavier is a sound encoded above both its source's known weight and the lightest target.
	heavier int
	// channels and sound prefer the richest sound, then the lightest.
	channels int
	sound    media.Bitrate
}

func standingOf(in Inputs, p Plan) standing {
	var s standing
	axes := p.Encoded()
	if axes.Video {
		s.cost += 2
	}
	if axes.Audio {
		s.cost++
	}
	if in.MaxBitrate == 0 {
		return s
	}
	if slices.Contains(p.Refusals, reasonBitrateFloor) {
		s.floored = 1
	}
	sound := weighs(in, p.Audio)
	if v, ok := p.Video.Encode(); !ok || v.Bitrate < streamBudget(in.MaxBitrate)-sound {
		return s
	}
	s.squeezed = 1
	if source := in.Probe.AudioBitrate; source > 0 && sound > max(source, floorAudio.bitrate) {
		s.heavier = 1
	}
	a, _ := p.Audio.Encode()
	s.channels, s.sound = a.Channels, sound
	return s
}

func (s standing) before(o standing) bool {
	return cmp.Or(cmp.Compare(s.floored, o.floored), cmp.Compare(s.cost, o.cost), cmp.Compare(s.squeezed, o.squeezed),
		cmp.Compare(s.heavier, o.heavier), cmp.Compare(o.channels, s.channels), cmp.Compare(s.sound, o.sound)) < 0
}
