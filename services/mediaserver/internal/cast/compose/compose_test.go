package compose

import (
	"net/url"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

func TestTheCompositionADeviceGets(t *testing.T) {
	selfFetching := media.Capabilities{SelfFetch: true, Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}}
	hlsFetching := media.Capabilities{SelfFetch: true, Formats: []media.FormatSupport{{Transport: media.TransportHLS, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}}
	pushOnly, pushOnlyHLS := selfFetching, hlsFetching
	pushOnly.SelfFetch, pushOnlyHLS.SelfFetch = false, false
	for _, tt := range []struct {
		name       string
		device     media.Capabilities
		sourceCT   string
		wrapped    bool
		preference DeliveryPreference
		want       Kind
	}{
		{"a push-only device is read once", media.Capabilities{}, media.MP4, false, DeliveryAuto, ReadOnce},
		{"a self-fetching device that accepts the source is handed the URL", selfFetching, media.MP4, false, DeliveryAuto, Handoff},
		{"ordinary HLS is handed to a capable device", hlsFetching, media.HLS, false, DeliveryAuto, Handoff},
		{"observed wrapped HLS is relayed", hlsFetching, media.HLS, true, DeliveryAuto, Remux},
		{"a rejected container is remuxed", selfFetching, media.MKV, false, DeliveryAuto, Remux},
		{"the serve preference relays what could have been handed over", selfFetching, media.MP4, false, DeliveryServe, Remux},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := For(Shape{Device: tt.device, Program: measured(t, tt.sourceCT, tt.wrapped), Delivery: tt.preference}); got != tt.want {
				t.Errorf("composed %q, want %q", got, tt.want)
			}
		})
	}
	for _, tt := range []struct {
		name       string
		device     media.Capabilities
		sourceCT   string
		native     bool
		preference DeliveryPreference
		want       Kind
	}{
		{"a Native cast on a push-only device that plays the source gets its own bytes", pushOnly, media.MP4, true, DeliveryAuto, Forward},
		{"an optimized cast stays served, so castor keeps pacing it", pushOnly, media.MP4, false, DeliveryAuto, ReadOnce},
		{"a push-only device is served once it refused the source", pushOnly, media.MP4, true, DeliveryServe, ReadOnce},
		{"a container the push-only device does not name is read once", pushOnly, media.MKV, true, DeliveryAuto, ReadOnce},
		{"a playlist is never forwarded", pushOnlyHLS, media.HLS, true, DeliveryAuto, ReadOnce},
	} {
		t.Run(tt.name, func(t *testing.T) {
			shape := Shape{Device: tt.device, Program: measured(t, tt.sourceCT, false), Delivery: tt.preference, PreserveSource: tt.native}
			if got := For(shape); got != tt.want {
				t.Errorf("composed %q, want %q", got, tt.want)
			}
		})
	}
}

// measured is a header-free H.264/AAC program in contentType, measured as such.
func measured(t *testing.T, contentType string, wrapped bool) media.Program {
	t.Helper()
	candidate := &source.Stream{
		URL:         &url.URL{Scheme: "https", Host: "cdn.example", Path: "/movie"},
		ContentType: contentType,
		Probe:       &media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, AudioCodec: media.CodecAAC, AudioChannels: 2, SourceUnwrapped: wrapped},
	}
	program, err := source.ProgramFor(candidate)
	if err != nil {
		t.Fatal(err)
	}
	program.SetMeasurement(*candidate.Probe)
	return program
}
