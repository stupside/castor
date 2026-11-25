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
	for _, tt := range []struct {
		name       string
		device     media.Capabilities
		sourceCT   string
		wrapped    media.HLSExtraSegmentFormat
		preference DeliveryPreference
		want       Kind
	}{
		{"a push-only device is read once", media.Capabilities{}, media.MP4, 0, DeliveryAuto, ReadOnce},
		{"a self-fetching device that accepts the source is handed the URL", selfFetching, media.MP4, 0, DeliveryAuto, Handoff},
		{"ordinary HLS is handed to a capable device", hlsFetching, media.HLS, 0, DeliveryAuto, Handoff},
		{"observed wrapped HLS is relayed", hlsFetching, media.HLS, media.HLSExtraSegmentTIKTIKPX, DeliveryAuto, Remux},
		{"a rejected container is remuxed", selfFetching, media.MKV, 0, DeliveryAuto, Remux},
		{"the serve preference relays what could have been handed over", selfFetching, media.MP4, 0, DeliveryServe, Remux},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidate := &source.Stream{
				URL:         &url.URL{Scheme: "https", Host: "cdn.example", Path: "/movie"},
				ContentType: tt.sourceCT,
				Probe:       &media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, AudioCodec: media.CodecAAC, AudioChannels: 2, SourceSegments: tt.wrapped},
			}
			program, err := source.ProgramFor(candidate)
			if err != nil {
				t.Fatal(err)
			}
			program.SetMeasurement(*candidate.Probe)
			if got := For(Shape{Device: tt.device, Program: program, Delivery: tt.preference}); got != tt.want {
				t.Errorf("composed %q, want %q", got, tt.want)
			}
		})
	}
}
