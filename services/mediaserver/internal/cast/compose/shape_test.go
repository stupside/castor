package compose

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestAHandoffOnlyWhenEverythingChecks(t *testing.T) {
	caps := media.Capabilities{SelfFetch: true, Formats: []media.FormatSupport{
		{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}},
		{Transport: media.TransportHLS, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}},
	}}
	for _, tt := range []struct {
		name          string
		contentType   string
		headers       http.Header
		unmeasured    bool
		declaredOnly  bool
		height        int
		maxHeight     media.HeightCap
		ceiling, rate media.Bitrate
		wrappedHLS    bool
		sourceWrapped bool
		want          bool
	}{
		{name: "a header-free measured file at the ceiling passes through", contentType: media.MP4, height: 1080, maxHeight: 1080, want: true},
		{name: "ordinary HLS passes through", contentType: media.HLS, height: 1080, maxHeight: 1080, want: true},
		{name: "observed wrapped HLS stays local", contentType: media.HLS, height: 1080, maxHeight: 1080, sourceWrapped: true},
		{name: "a device supporting wrapped segments can fetch HLS directly", contentType: media.HLS, height: 1080, maxHeight: 1080, wrappedHLS: true, want: true},
		{name: "a device supporting wrapped segments can fetch observed wrapped HLS", contentType: media.HLS, height: 1080, maxHeight: 1080, wrappedHLS: true, sourceWrapped: true, want: true},
		{name: "an unmeasured program is not assumed decodable", contentType: media.MP4, unmeasured: true},
		{name: "a manifest declaration cannot authorize handoff", contentType: media.HLS, height: 1080, maxHeight: 1080, declaredOnly: true},
		{name: "a header-gated source is served", contentType: media.MP4, headers: http.Header{"Referer": {"https://player.example/"}}},
		{name: "a source above the ceiling is served, since a hand-off cannot be downscaled", contentType: media.MP4, height: 1600, maxHeight: 1080},
		{name: "a source of unknown video height is served when capped", contentType: media.HLS, maxHeight: 480},
		{name: "a source declared under the rate ceiling passes through", contentType: media.HLS, height: 1080, maxHeight: 1080, ceiling: 4_000_000, rate: 3_000_000, want: true},
		{name: "a source declared over the rate ceiling is served, since a hand-off cannot be lightened", contentType: media.HLS, height: 1080, maxHeight: 1080, ceiling: 4_000_000, rate: 6_000_000},
		{name: "a source declaring no rate is served under a rate ceiling", contentType: media.HLS, height: 1080, maxHeight: 1080, ceiling: 4_000_000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			program, err := media.NewProgram(media.Program{
				Inputs: []media.Input{{
					ID: media.PrimaryInputID, URL: &url.URL{Scheme: "https", Host: "cdn.example", Path: "/media"},
					ContentType: tt.contentType, Headers: tt.headers,
				}},
				Tracks: []media.TrackRef{
					{Input: media.PrimaryInputID, Kind: media.TrackVideo, Optional: true},
					{Input: media.PrimaryInputID, Kind: media.TrackAudio, Optional: true},
				},
				ClockInput: media.PrimaryInputID,
				EndPolicy:  media.EndAtLongest,
			})
			if err != nil {
				t.Fatal(err)
			}
			if tt.declaredOnly {
				program.SetDeclaration(media.Declaration{VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoHeight: 1080, AudioCodec: media.CodecAAC})
			} else if !tt.unmeasured {
				program.SetMeasurement(media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, AudioCodec: media.CodecAAC, AudioChannels: 2, SourceUnwrapped: tt.sourceWrapped})
			}
			device := caps
			device.Formats = append([]media.FormatSupport(nil), caps.Formats...)
			if tt.wrappedHLS {
				device.Formats[1].ExtraSegments = []media.HLSExtraSegmentFormat{media.HLSExtraSegmentTIKTIKPX}
			}
			shape := Shape{Device: device, Program: program, Height: tt.height, MaxHeight: tt.maxHeight, MaxBitrate: tt.ceiling, Rate: tt.rate}
			if got := shape.handoff(); got != tt.want {
				t.Errorf("handoff() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestANativeOriginalReachesATrialReceiverAsItIs(t *testing.T) {
	trial := media.Capabilities{SelfFetch: true, NativePlaybackTrial: true, Formats: []media.FormatSupport{
		{Transport: media.TransportHLS, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}},
		{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}},
	}}
	dolbyVision := media.ProbeInfo{VideoCodec: media.CodecHEVC, VideoBitDepth: 10, VideoHDR: true, VideoHeight: 1608, AudioCodec: media.CodecAAC, AudioChannels: 2}
	for _, tt := range []struct {
		name        string
		contentType string
		probe       media.ProbeInfo
		preserve    bool
		want        bool
	}{
		{"a Native Dolby Vision file is handed to the receiver, which judges it", media.MP4, dolbyVision, true, true},
		{"an Optimized HDR source is still served", media.MP4, dolbyVision, false, false},
		{"a container the receiver never named is served", media.MKV, dolbyVision, true, false},
		{"a Native HLS source with an envelope the receiver never named stays local", media.HLS, func() media.ProbeInfo { p := dolbyVision; p.SourceUnwrapped = true; return p }(), true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			program, err := media.NewProgram(media.Program{
				Inputs:     []media.Input{{ID: media.PrimaryInputID, URL: &url.URL{Scheme: "https", Host: "cdn.example", Path: "/media"}, ContentType: tt.contentType}},
				Tracks:     []media.TrackRef{{Input: media.PrimaryInputID, Kind: media.TrackVideo, Optional: true}, {Input: media.PrimaryInputID, Kind: media.TrackAudio, Optional: true}},
				ClockInput: media.PrimaryInputID,
				EndPolicy:  media.EndAtLongest,
			})
			if err != nil {
				t.Fatal(err)
			}
			program.SetMeasurement(tt.probe)
			shape := Shape{Device: trial, Program: program, Height: tt.probe.VideoHeight, PreserveSource: tt.preserve}
			if got := shape.handoff(); got != tt.want {
				t.Errorf("handoff() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCastorForwardsOnlyAFinishedFileTheDeviceMayPlay(t *testing.T) {
	// A renderer naming Matroska without codecs, like an LG webOS DLNA sink.
	trial := media.Capabilities{NativePlaybackTrial: true, Formats: []media.FormatSupport{
		{Transport: media.TransportMKV, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}},
	}}
	dolbyVision := media.ProbeInfo{VideoCodec: media.CodecHEVC, VideoBitDepth: 10, VideoHDR: true, VideoHeight: 2160,
		VideoDolbyVision: &media.DolbyVision{Profile: 8, Level: 6, Compatibility: 1}, AudioCodec: media.CodecEAC3, AudioChannels: 6}
	for _, tt := range []struct {
		name        string
		device      media.Capabilities
		contentType string
		headers     http.Header
		live        bool
		start       time.Duration
		preserve    bool
		height      int
		maxHeight   media.HeightCap
		want        bool
	}{
		{name: "Native gives a trial renderer the Dolby Vision file it names the container of", device: trial, contentType: media.MKV, preserve: true, want: true},
		{name: "the source's own request headers travel with castor, not the device", device: trial, contentType: media.MKV, preserve: true, headers: http.Header{"Referer": {"https://player.example/"}}, want: true},
		{name: "outside Native the renderer's family defaults still decide", device: trial, contentType: media.MKV},
		{name: "a container the renderer does not name is converted", device: trial, contentType: media.MP4, preserve: true},
		{name: "a live source is not a file to seek in", device: trial, contentType: media.MKV, preserve: true, live: true},
		{name: "a cast from a later position is served from there", device: trial, contentType: media.MKV, preserve: true, start: time.Minute},
		{name: "a picture over the ceiling cannot be lightened by forwarding", device: trial, contentType: media.MKV, preserve: true, height: 2160, maxHeight: 1080},
		{name: "a playlist is never one file", device: trial, contentType: media.HLS, preserve: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			program, err := media.NewProgram(media.Program{
				Inputs: []media.Input{{
					ID: media.PrimaryInputID, URL: &url.URL{Scheme: "https", Host: "cdn.example", Path: "/movie"},
					ContentType: tt.contentType, Headers: tt.headers, Fetch: media.Fetch{Live: tt.live},
				}},
				Tracks: []media.TrackRef{
					{Input: media.PrimaryInputID, Kind: media.TrackVideo, Optional: true},
					{Input: media.PrimaryInputID, Kind: media.TrackAudio, Optional: true},
				},
				ClockInput: media.PrimaryInputID,
				EndPolicy:  media.EndAtLongest,
				Start:      tt.start,
			})
			if err != nil {
				t.Fatal(err)
			}
			program.SetMeasurement(dolbyVision)
			shape := Shape{Device: tt.device, Program: program, PreserveSource: tt.preserve, Height: tt.height, MaxHeight: tt.maxHeight}
			if got := shape.forwards(); got != tt.want {
				t.Errorf("forwards() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAHandedOverProgramIsOneTheDevicePlaysWhole(t *testing.T) {
	r := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}}
	for _, tt := range []struct {
		name string
		info media.ProbeInfo
		want bool
	}{
		{"supported video without audio", media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8}, true},
		{"supported audio without video", media.ProbeInfo{AudioCodec: media.CodecAAC, AudioChannels: 2}, true},
		{"no measured tracks", media.ProbeInfo{}, false},
		{"unsupported audio", media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, AudioCodec: media.CodecEAC3, AudioChannels: 6}, false},
		{"HDR is never assumed safe", media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoHDR: true}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.CanPlaySourceIn(media.MP4, tt.info); got != tt.want {
				t.Errorf("CanPlaySourceIn = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMeasuredAudioOnlySourceCanPassThrough(t *testing.T) {
	program, err := media.NewProgram(media.Program{
		Inputs: []media.Input{{
			ID: media.PrimaryInputID, URL: &url.URL{Scheme: "https", Host: "cdn.example", Path: "/audio.mp4"},
			ContentType: media.MP4,
		}},
		Tracks:     []media.TrackRef{{Input: media.PrimaryInputID, Kind: media.TrackAudio}},
		ClockInput: media.PrimaryInputID,
		EndPolicy:  media.EndAtLongest,
	})
	if err != nil {
		t.Fatal(err)
	}
	program.SetMeasurement(media.ProbeInfo{AudioCodec: media.CodecAAC, AudioChannels: 2})
	shape := Shape{
		Device: media.Capabilities{SelfFetch: true, Formats: []media.FormatSupport{{
			Transport: media.TransportMP4, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2},
		}}},
		Program: program,
	}
	if !shape.handoff() {
		t.Fatal("compatible audio-only source was not handed directly to the receiver")
	}
}
