package codec

import (
	"context"
	"slices"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func hostEncoders(_ context.Context, codec media.Codec) (ffmpeg.Encoder, bool) {
	switch codec {
	case media.CodecHEVC:
		return ffmpeg.Encoder{Name: "hevc_videotoolbox", Codec: media.CodecHEVC, Hardware: true}, true
	case media.CodecH264:
		return ffmpeg.Encoder{Name: "libx264", Codec: media.CodecH264}, true
	}
	return ffmpeg.Encoder{}, false
}

func TestSelectVideoEncoder(t *testing.T) {
	decodes := func(codecs ...media.Codec) media.Capabilities {
		var r media.Capabilities
		for _, c := range codecs {
			r.Formats = append(r.Formats, media.FormatSupport{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: c}})
		}
		return r
	}
	hevcHW := ffmpeg.Encoder{Name: "hevc_videotoolbox", Codec: media.CodecHEVC, Hardware: true}
	hevcSW := ffmpeg.Encoder{Name: "libx265", Codec: media.CodecHEVC}
	h264HW := ffmpeg.Encoder{Name: "h264_videotoolbox", Codec: media.CodecH264, Hardware: true}
	h264SW := ffmpeg.Encoder{Name: "libx264", Codec: media.CodecH264}

	for _, tt := range []struct {
		name  string
		caps  media.Capabilities
		avail map[media.Codec]ffmpeg.Encoder
		want  string
	}{
		{"hardware HEVC first", decodes(media.CodecHEVC, media.CodecH264), map[media.Codec]ffmpeg.Encoder{media.CodecHEVC: hevcHW, media.CodecH264: h264HW}, "hevc_videotoolbox"},
		{"software HEVC is never run live", decodes(media.CodecHEVC, media.CodecH264), map[media.Codec]ffmpeg.Encoder{media.CodecHEVC: hevcSW, media.CodecH264: h264HW}, "h264_videotoolbox"},
		{"only what the device decodes", decodes(media.CodecH264), map[media.Codec]ffmpeg.Encoder{media.CodecHEVC: hevcHW, media.CodecH264: h264SW}, "libx264"},
		{"nothing in common", decodes(media.CodecVP8), map[media.Codec]ffmpeg.Encoder{media.CodecH264: h264SW}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, _, _ := selectVideoEncoder(tt.caps, func(c media.Codec) (ffmpeg.Encoder, bool) { e, ok := tt.avail[c]; return e, ok })
			if got.Name != tt.want {
				t.Errorf("selectVideoEncoder = %q, want %q", got.Name, tt.want)
			}
		})
	}
}

func TestWhatForcesAVideoReEncode(t *testing.T) {
	h264 := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}
	hdrCapable := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecHEVC, Profiles: []media.Profile{"Main 10"}, BitDepths: []int{8, 10}}}, {Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}
	sdr := media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 720, VideoBitDepth: 8}
	tall := media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 2160, VideoBitDepth: 8}
	hdr := media.ProbeInfo{VideoCodec: media.CodecHEVC, VideoProfile: "Main 10", VideoHeight: 720, VideoBitDepth: 10, VideoHDR: true}

	for _, tt := range []struct {
		name   string
		caps   media.Capabilities
		probe  media.ProbeInfo
		burnIn string
		want   []Reason
	}{
		{name: "nothing against it copies", caps: h264, probe: sdr},
		{name: "a source above the ceiling", caps: h264, probe: tall, want: []Reason{reasonHeightLimit}},
		{name: "HDR even on a device that decodes it", caps: hdrCapable, probe: hdr, want: []Reason{reasonHDRPolicy}},
		{name: "a burn-in needs decoded frames", caps: h264, probe: sdr, burnIn: "/tmp/cue.txt", want: []Reason{reasonSubtitleBurnIn}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := Inputs{Caps: tt.caps, Probe: tt.probe, Into: testFormat(t, media.MPEGTS), MaxHeight: 1080, BurnIn: tt.burnIn, Encoders: hostEncoders}
			track, refused, err := decideVideo(t.Context(), in, 0)
			if err != nil {
				t.Fatalf("decideVideo: %v", err)
			}
			if !slices.Equal(refused, tt.want) {
				t.Errorf("reasons = %v, want %v", refused, tt.want)
			}
			if enc, ok := track.Encode(); ok && (enc.MaxHeight != in.MaxHeight || enc.SubtitleTextFile != tt.burnIn) {
				t.Errorf("re-encode carries ceiling %d and burn-in %q, want %d and %q", enc.MaxHeight, enc.SubtitleTextFile, in.MaxHeight, tt.burnIn)
			}
		})
	}
}

func TestARateCeilingForcesAnEncodeOnlyWhereACopyIsNotProvenUnderIt(t *testing.T) {
	h264 := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}
	sdr := media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 720, VideoBitDepth: 8}

	for _, tt := range []struct {
		name          string
		ceiling, rate media.Bitrate
		want          []Reason
	}{
		{name: "no ceiling copies whatever it weighs", rate: 20_000_000},
		{name: "a copy under the ceiling is kept", ceiling: 4_000_000, rate: 3_000_000},
		{name: "a copy over the ceiling is encoded", ceiling: 4_000_000, rate: 6_500_000, want: []Reason{reasonBitrateLimit}},
		{name: "a copy nobody weighed proves nothing under a ceiling", ceiling: 4_000_000, want: []Reason{reasonBitrateLimit}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := Inputs{Caps: h264, Probe: sdr, Into: testFormat(t, media.MPEGTS), MaxHeight: 1080, MaxBitrate: tt.ceiling, Rate: tt.rate, Encoders: hostEncoders}
			_, refused, err := decideVideo(t.Context(), in, 0)
			if err != nil {
				t.Fatalf("decideVideo: %v", err)
			}
			if !slices.Equal(refused, tt.want) {
				t.Errorf("reasons = %v, want %v", refused, tt.want)
			}
		})
	}
}

func TestAnEncodeUnderARateCeilingWeighsLessThanItsCeilingLessTheSound(t *testing.T) {
	h264 := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}
	sdr := media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 720, VideoBitDepth: 8}
	in := Inputs{Caps: h264, Probe: sdr, Into: testFormat(t, media.MPEGTS), MaxHeight: 1080, MaxBitrate: 2_700_000, Rate: 6_500_000, Encoders: hostEncoders}

	track, _, err := decideVideo(t.Context(), in, 448_000)
	if err != nil {
		t.Fatalf("decideVideo: %v", err)
	}
	enc, ok := track.Encode()
	if !ok {
		t.Fatal("a copy over the ceiling was kept")
	}
	// 95% of 2.7 Mbit/s, less the 448 kbit/s of sound.
	if want := media.Bitrate(2_117_000); enc.Bitrate != want || enc.Maxrate != want {
		t.Errorf("the encode is %d at most %d, want %d", enc.Bitrate, enc.Maxrate, want)
	}

	// Unbounded, a picture the height ceiling forces to an encode takes the target's own rate.
	in.MaxBitrate, in.Probe.VideoHeight = 0, 2160
	track, _, _ = decideVideo(t.Context(), in, 448_000)
	if enc, _ := track.Encode(); enc.Bitrate != 4_000_000 {
		t.Errorf("an unbounded encode is %d, want the target's own 4000000", enc.Bitrate)
	}
}

func TestADeliveryRunningKeepsItsAudioCodecWhenItsRateChanges(t *testing.T) {
	caps := media.Capabilities{Formats: []media.FormatSupport{
		{Transport: media.TransportMPEGTS, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAC3}},
		{Transport: media.TransportMPEGTS, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC}},
	}}
	in := Inputs{Caps: caps, Probe: media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoHeight: 1080, AudioCodec: media.CodecAAC, AudioChannels: 2, AudioBitrate: 128_000}, Measured: true, MaxHeight: 1080, MaxBitrate: 3_000_000, Rate: 6_500_000, Into: testFormat(t, media.MPEGTS), Encoders: hostEncoders}
	plan, err := Served(t.Context(), in)
	if err != nil || plan.Audio.Action() != CopyTrack {
		t.Fatalf("audio = %s, %v; want the cheapest, a copy", plan.Audio.Name(), err)
	}
	in.KeepAudio = media.CodecAC3
	plan, err = Served(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if a, encoded := plan.Audio.Encode(); !encoded || a.Codec != media.CodecAC3 {
		t.Errorf("audio = %s, want ac3 as the delivery began", plan.Audio.Name())
	}
}

func TestAThinRateIsSpentOnASmallerPicture(t *testing.T) {
	h264 := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}
	sdr := media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoHeight: 1080, VideoProfile: media.ProfileHigh}
	for _, tt := range []struct {
		ceiling    media.Bitrate
		wantHeight media.HeightCap
		wantFPS    float64
	}{
		{4_000_000, 1080, 60},
		{2_000_000, 720, 30},
		{1_500_000, 540, 30},
		{1_000_000, 432, 30},
	} {
		in := Inputs{Caps: h264, Probe: sdr, Into: testFormat(t, media.MPEGTS), MaxHeight: 1080, MaxBitrate: tt.ceiling, Rate: 8_000_000, Encoders: hostEncoders}
		track, _, err := decideVideo(t.Context(), in, 448_000)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := track.Encode()
		if v.MaxHeight != tt.wantHeight || v.MaxFrameRate != tt.wantFPS {
			t.Errorf("under %s: %dp at %v fps, want %dp at %v", tt.ceiling, v.MaxHeight, v.MaxFrameRate, tt.wantHeight, tt.wantFPS)
		}
	}
}
