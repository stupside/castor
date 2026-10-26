package codec

import (
	"strings"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestEncoderCarriesReceiverProfileAndLevel(t *testing.T) {
	in := Inputs{Caps: media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264, Profiles: []media.Profile{media.ProfileBaseline}, MaxLevel: 31}}}}, Probe: media.ProbeInfo{VideoCodec: media.CodecH264, VideoProfile: media.ProfileHigh, VideoBitDepth: 8, VideoHeight: 2160}, MaxHeight: 2160, Into: testFormat(t, media.MPEGTS), Encoders: hostEncoders}
	track, _, err := decideVideo(t.Context(), in, 0)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := track.Encode()
	if !ok || v.Profile != "baseline" || v.Level != "3.1" || v.MaxWidth != 1280 || v.MaxHeight != 720 || v.MaxFrameRate != 30 {
		t.Fatalf("encode=%+v", v)
	}
	in.Caps.Formats[0].Video.MaxLevel = 20
	if _, _, err := decideVideo(t.Context(), in, 0); err == nil {
		t.Fatal("unsupported low envelope invented")
	}
	in.Caps.Formats[0].Video.MaxLevel = 42
	in.Caps.Formats[0].Video.BitDepths = []int{10}
	if _, _, err := decideVideo(t.Context(), in, 0); err == nil {
		t.Fatal("8-bit output chosen for 10-bit-only envelope")
	}
}
func TestPlanNeverCombinesDifferentReceiverTuples(t *testing.T) {
	v := media.VideoSupport{Codec: media.CodecH264}
	caps := media.Capabilities{Formats: []media.FormatSupport{
		{Transport: media.TransportMPEGTS, Video: v, Audio: media.AudioSupport{Codec: media.CodecAC3}},
		{Transport: media.TransportMP4, Video: v, Audio: media.AudioSupport{Codec: media.CodecAAC}},
	}}
	in := Inputs{Caps: caps, Probe: media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoHeight: 720, AudioCodec: media.CodecAAC, AudioChannels: 2}, Measured: true, MaxHeight: 1080, Into: testFormat(t, media.MPEGTS), Encoders: hostEncoders}
	plan, err := Served(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	a, encoded := plan.Audio.Encode()
	if !encoded || a.Codec != media.CodecAC3 {
		t.Fatalf("TS/AAC silently admitted: %+v", plan)
	}
	if caps.CanPlayIn(media.MPEGTS, in.Probe) {
		t.Fatal("direct TS/AAC admitted")
	}
	if !caps.CanPlayIn(media.MP4, in.Probe) {
		t.Fatal("valid MP4/AAC tuple refused")
	}
}

func TestADeviceWithNoFormatForTheContainerIsNotBlamedOnAKeptSound(t *testing.T) {
	caps := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}
	_, err := Served(t.Context(), Inputs{Caps: caps, Probe: media.ProbeInfo{VideoCodec: media.CodecH264}, Into: testFormat(t, media.MPEGTS), Encoders: hostEncoders})
	if err == nil || strings.Contains(err.Error(), "kept") {
		t.Errorf("Served = %v, want a refusal naming the container, not a kept sound nobody kept", err)
	}
}

func TestUnsetUserHeightCannotBypassEncoderEnvelope(t *testing.T) {
	if got := boundedHeight(0, 720); got != 720 {
		t.Fatalf("height=%d", got)
	}
	if got := boundedHeight(480, 720); got != 480 {
		t.Fatalf("height=%d", got)
	}
}

func TestTieKeepsVideoCopiedOverAudio(t *testing.T) {
	main := media.VideoSupport{Codec: media.CodecH264, Profiles: []media.Profile{media.ProfileMain}, MaxLevel: 42}
	any := media.VideoSupport{Codec: media.CodecH264, MaxLevel: 42}
	caps := media.Capabilities{Formats: []media.FormatSupport{
		{Transport: media.TransportMPEGTS, Video: main, Audio: media.AudioSupport{Codec: media.CodecAAC}},
		{Transport: media.TransportMPEGTS, Video: any, Audio: media.AudioSupport{Codec: media.CodecAC3}},
	}}
	in := Inputs{Caps: caps, Probe: media.ProbeInfo{VideoCodec: media.CodecH264, VideoProfile: media.ProfileHigh, VideoLevel: 40, VideoBitDepth: 8, VideoHeight: 1080, AudioCodec: media.CodecAAC, AudioChannels: 2}, Measured: true, MaxHeight: 1080, Into: testFormat(t, media.MPEGTS), Encoders: hostEncoders}
	plan, err := Served(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if encoded := plan.Encoded(); encoded.Video || !encoded.Audio {
		t.Fatalf("picture re-encoded to spare the sound: %+v", plan)
	}
}
