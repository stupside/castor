package codec

import (
	"cmp"
	"context"
	"fmt"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Floor plans read-once encoding: which halves produce vs. pass through.
func Floor(ctx context.Context, in Inputs) (Plan, error) {
	video, videoRefusals, err := floorVideoTrack(ctx, in)
	if err != nil {
		return Plan{}, fmt.Errorf("planning the buffer's video: %w", err)
	}
	audio, audioRefusals := floorAudioTrack(in)
	return Plan{Video: video, Audio: audio, Refusals: append(videoRefusals, audioRefusals...)}, nil
}

// Video half: stream copy or floor codec under VBV cap, scaled to ceiling.
func floorVideoTrack(ctx context.Context, in Inputs) (Track[VideoEncode], []Reason, error) {
	if in.Measured && !in.Probe.HasVideo() {
		return OmitVideo(), nil, nil
	}
	refused := refuse(videoCarriageRefusals, in)
	if len(refused) == 0 {
		return CopyVideo(), nil, nil
	}
	enc, ok := in.Encoders(ctx, floorVideo.codec, in.Software)
	if !ok {
		return Track[VideoEncode]{}, refused, fmt.Errorf("no working encoder for recovery codec %q", floorVideo.codec)
	}

	produced := VideoEncode{
		Encoder: enc,
		Maxrate: floorVideo.maxrate,
		Bufsize: floorVideo.bufsize,
		// Caps pixel rate where VBV cap cannot; prevents false slowness reports.
		MaxHeight: in.MaxHeight,
	}
	// Hardware uses bitrate, software uses quality; not interchangeable.
	if enc.Hardware {
		produced.Bitrate = floorVideo.bitrate
	} else {
		produced.Quality = floorVideoQuality
	}
	return Encode(produced), refused, nil
}

// Audio half: stream copy, else surround kept in the buffer's surround codec and stereo in AAC; cannot fail.
func floorAudioTrack(in Inputs) (Track[AudioEncode], []Reason) {
	if in.Measured && !in.Probe.HasAudio() {
		return OmitAudio(), nil
	}
	refused := refuse(audioCarriageRefusals, in)
	if len(refused) == 0 {
		return CopyAudio(), nil
	}
	// The device is not known yet, so the buffer keeps what a surround device could still be given.
	target := floorAudio
	if in.Probe.AudioChannels > floorAudio.maxChannels {
		target = surroundTargets[0]
	}
	return Encode(AudioEncode{
		Codec:      target.codec,
		Bitrate:    target.bitrate,
		SampleRate: media.PlaybackSampleRate,
		Channels:   max(min(cmp.Or(in.Probe.AudioChannels, 2), target.maxChannels), 2),
		Resync:     in.Spliced,
	}), refused
}
