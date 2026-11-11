package codec

import (
	"context"
	"errors"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func originalInput(t *testing.T) Inputs {
	t.Helper()
	return Inputs{
		PreserveSource: true, Measured: true, MaxHeight: 2147483647,
		Probe: media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoHeight: 2160,
			AudioCodec: media.CodecAAC, AudioChannels: 2, AudioSampleRate: 48000},
		Caps: media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMPEGTS,
			Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}},
		Into: testFormat(t, media.MPEGTS),
		Encoders: func(context.Context, media.Codec, bool) (ffmpeg.Encoder, bool) {
			t.Fatal("a native cast must never ask for an encoder")
			return ffmpeg.Encoder{}, false
		},
	}
}

func TestNativeCopiesBothOriginalTracksWithoutRequestingAnEncoder(t *testing.T) {
	for _, plan := range []func(context.Context, Inputs) (Plan, error){Served, Floor} {
		p, err := plan(t.Context(), originalInput(t))
		if err != nil || p.Video.Action() != CopyTrack || p.Audio.Action() != CopyTrack || p.Encoded() != (media.Axes{}) {
			t.Fatalf("native plan = %+v, %v; want original video and audio", p, err)
		}
	}
}

func TestNativeTrialDefersUnverifiedHEVCHDRAudioSupportToTheReceiver(t *testing.T) {
	in := originalInput(t)
	in.Caps.NativePlaybackTrial = true
	in.Probe = media.ProbeInfo{
		VideoCodec: media.CodecHEVC, VideoProfile: media.ProfileMain10, VideoBitDepth: 10,
		VideoHeight: 1608, VideoHDR: true, AudioCodec: media.CodecAC3, AudioChannels: 6, AudioSampleRate: 48000,
	}
	plan, err := Served(t.Context(), in)
	if err != nil || plan.Video.Action() != CopyTrack || plan.Audio.Action() != CopyTrack || plan.Encoded() != (media.Axes{}) {
		t.Fatalf("native HDR trial = %+v, %v; want original HEVC Main10 HDR and AC3 tracks", plan, err)
	}
	if in.Caps.CanCopyVideo(in.Probe) || in.Caps.CanCopyAudio(in.Probe) {
		t.Fatal("a playback trial must not claim the unverified decoder is supported")
	}
	in.PreserveSource = false
	in.Encoders = func(context.Context, media.Codec, bool) (ffmpeg.Encoder, bool) {
		return ffmpeg.Encoder{Name: "libx264", Codec: media.CodecH264}, true
	}
	optimized, err := Served(t.Context(), in)
	if err != nil || !optimized.Encoded().Video {
		t.Fatalf("optimized = %+v, %v; want the existing compatibility conversion", optimized, err)
	}
	venc, _ := optimized.Video.Encode()
	if !venc.ToneMap {
		t.Fatal("native trial policy disabled optimized HDR conversion")
	}
}

func TestNativeTrialStillRefusesInvalidOrAlteredDelivery(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Inputs)
	}{
		{"unknown tracks", func(in *Inputs) { in.Measured = false }},
		{"unsupported muxer carriage", func(in *Inputs) { in.Probe.VideoCodec = media.CodecVP8 }},
		{"height limit", func(in *Inputs) { in.MaxHeight = 1080 }},
		{"burned subtitles", func(in *Inputs) { in.BurnIn = "captions.ass" }},
		{"decode requested", func(in *Inputs) { in.Decode.Video = true }},
		{"spliced source", func(in *Inputs) { in.Spliced = true }},
		{"no delivery transport", func(in *Inputs) { in.Caps.Formats = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := originalInput(t)
			in.Caps.NativePlaybackTrial = true
			tc.change(&in)
			if _, err := Served(t.Context(), in); !errors.Is(err, media.ErrNativeIncompatible) {
				t.Fatalf("native trial = %v; want refusal before delivery", err)
			}
		})
	}
}

func TestNativeRefusesRequiredConversionsInsteadOfDegradingTheSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Inputs)
	}{
		{"unknown tracks", func(in *Inputs) { in.Measured = false }},
		{"no advertised formats", func(in *Inputs) { in.Caps.Formats = nil }},
		{"HDR support unproven", func(in *Inputs) { in.Probe.VideoHDR = true }},
		{"unsupported video", func(in *Inputs) { in.Probe.VideoCodec = media.CodecHEVC }},
		{"unsupported audio", func(in *Inputs) { in.Probe.AudioCodec = media.CodecAC3 }},
		{"height cap", func(in *Inputs) { in.MaxHeight = 1080 }},
		{"sample rate", func(in *Inputs) { in.Probe.AudioSampleRate = 96000 }},
		{"burned subtitles", func(in *Inputs) { in.BurnIn = "captions.ass" }},
		{"failed video copy", func(in *Inputs) { in.Decode.Video = true }},
		{"failed audio copy", func(in *Inputs) { in.Decode.Audio = true }},
		{"spliced program", func(in *Inputs) { in.Spliced = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := originalInput(t)
			tc.change(&in)
			if _, err := Served(t.Context(), in); !errors.Is(err, media.ErrNativeIncompatible) {
				t.Fatalf("native cast = %v; want explicit incompatibility", err)
			}
		})
	}
}

func TestNativeBufferCannotConvertTracksBeforeDeliveryIsPlanned(t *testing.T) {
	for _, axes := range []media.Axes{{Video: true}, {Audio: true}, {Video: true, Audio: true}} {
		in := originalInput(t)
		in.Decode = axes
		if _, err := Floor(t.Context(), in); !errors.Is(err, media.ErrNativeIncompatible) {
			t.Fatalf("buffer with decode %+v = %v; want native incompatibility", axes, err)
		}
	}
}
