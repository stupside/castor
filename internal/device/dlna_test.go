package device

import (
	"net/url"
	"slices"
	"testing"

	"github.com/huin/goupnp"

	"github.com/stupside/castor/internal/media"
)

func TestFindService(t *testing.T) {
	service := func(serviceType string) goupnp.Service {
		return goupnp.Service{
			ServiceType: serviceType,
			ControlURL:  goupnp.URLField{URL: url.URL{Scheme: "http", Host: "192.0.2.10:2870", Path: "/control/" + serviceType}, Ok: true},
		}
	}
	root := func(services ...goupnp.Service) *goupnp.RootDevice {
		return &goupnp.RootDevice{Device: goupnp.Device{
			FriendlyName: "Test Renderer",
			UDN:          "uuid:test",
			Services:     services,
		}}
	}
	const (
		v1 = "urn:schemas-upnp-org:service:AVTransport:1"
		v2 = "urn:schemas-upnp-org:service:AVTransport:2"
		v3 = "urn:schemas-upnp-org:service:AVTransport:3"
		rc = "urn:schemas-upnp-org:service:RenderingControl:3"
		cm = "urn:schemas-upnp-org:service:ConnectionManager:3"
	)

	tests := []struct {
		name    string
		root    *goupnp.RootDevice
		service string
		want    string
	}{
		{
			name:    "version 1 service is driven as before",
			root:    root(service(v1)),
			service: "AVTransport",
			want:    v1,
		},
		{
			// Philips 50PUD6654/43 and similar sets publish the v3 service only.
			name:    "version 3 only renderer is reachable",
			root:    root(service(rc), service(cm), service(v3)),
			service: "AVTransport",
			want:    v3,
		},
		{
			name:    "version 2 only renderer is reachable",
			root:    root(service(v2)),
			service: "AVTransport",
			want:    v2,
		},
		{
			name:    "newest published version wins",
			root:    root(service(v1), service(v2), service(v3)),
			service: "AVTransport",
			want:    v3,
		},
		{
			name: "service nested in a sub-device is found",
			root: &goupnp.RootDevice{Device: goupnp.Device{
				FriendlyName: "Test Renderer",
				Devices:      []goupnp.Device{{Services: []goupnp.Service{service(v3)}}},
			}},
			service: "AVTransport",
			want:    v3,
		},
		{
			name:    "renderer without any AVTransport is rejected",
			root:    root(service(rc), service(cm)),
			service: "AVTransport",
			want:    "",
		},
		{
			// Capability negotiation is version-locked the same way: a v3-only
			// ConnectionManager would otherwise degrade to fallbackCaps and
			// re-encode a stream the renderer could have copied.
			name:    "version 3 ConnectionManager is reachable for negotiation",
			root:    root(service(rc), service(cm), service(v3)),
			service: "ConnectionManager",
			want:    cm,
		},
	}

	loc := &url.URL{Scheme: "http", Host: "192.0.2.10:2870", Path: "/dmr.xml"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findService(tt.root, loc, tt.service)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("findService() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("findService() error = %v, want nil", err)
			}
			if got.Service.ServiceType != tt.want {
				t.Errorf("findService() service = %q, want %q", got.Service.ServiceType, tt.want)
			}
			// The SOAP action namespace must match the published version, or a
			// strict renderer rejects the call as an invalid action.
			if got.SOAPClient == nil {
				t.Error("findService() returned a client with no SOAPClient")
			}
		})
	}
}

func TestDLNAInfo(t *testing.T) {
	rootDevice := func(name string) *goupnp.RootDevice {
		return &goupnp.RootDevice{Device: goupnp.Device{FriendlyName: name}}
	}
	location := func(host, path string) *url.URL {
		return &url.URL{Scheme: "http", Host: host, Path: path}
	}

	tests := []struct {
		name   string
		result goupnp.MaybeRootDevice
		want   Info
		ok     bool
	}{
		{
			name:   "named renderer with a location",
			result: goupnp.MaybeRootDevice{Root: rootDevice("Living Room TV"), Location: location("192.0.2.10:8200", "/rootDesc.xml")},
			want:   Info{Name: "Living Room TV", Type: TypeDLNA, Address: "http://192.0.2.10:8200/rootDesc.xml"},
			ok:     true,
		},
		{
			name:   "second renderer maps independently",
			result: goupnp.MaybeRootDevice{Root: rootDevice("Bedroom Speaker"), Location: location("192.0.2.20:49152", "/desc.xml")},
			want:   Info{Name: "Bedroom Speaker", Type: TypeDLNA, Address: "http://192.0.2.20:49152/desc.xml"},
			ok:     true,
		},
		{
			name:   "announcement without a root device is rejected",
			result: goupnp.MaybeRootDevice{Location: location("192.0.2.30:8200", "/desc.xml")},
			ok:     false,
		},
		{
			name:   "announcement without a location is rejected",
			result: goupnp.MaybeRootDevice{Root: rootDevice("Ghost")},
			ok:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := dlnaInfo(tt.result)
			if ok != tt.ok {
				t.Fatalf("dlnaInfo() ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("dlnaInfo() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseSinkProtocolInfo(t *testing.T) {
	// A representative Samsung-style Sink: many AVC/MPEG entries, no HEVC token.
	avcSink := "http-get:*:audio/mpeg:*," +
		"http-get:*:video/mp2t:DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO," +
		"http-get:*:video/mp4:DLNA.ORG_PN=AVC_MP4_MP_HD_AAC," +
		"http-get:*:video/x-msvideo:*"

	caps := parseSinkProtocolInfo(avcSink)
	if !caps.SupportsCodec(media.CodecH264) {
		t.Error("AVC sink should advertise H.264")
	}
	if caps.SupportsCodec(media.CodecHEVC) {
		t.Error("AVC-only sink must not advertise HEVC")
	}
	if !slices.Contains(caps.Containers, "video/mp2t") {
		t.Errorf("expected video/mp2t container, got %v", caps.Containers)
	}

	// The H.264 envelope copies a codec-safe stream, not a 10-bit one.
	safe := media.ProbeInfo{VideoCodec: media.CodecH264, VideoProfile: "High", VideoBitDepth: 8}
	if !caps.CanCopyVideo(safe) {
		t.Error("codec-safe High H.264 should be copy-eligible")
	}
	tenBit := safe
	tenBit.VideoBitDepth = 10
	if caps.CanCopyVideo(tenBit) {
		t.Error("10-bit H.264 must not be copy-eligible")
	}

	// A renderer advertising an HEVC TS profile lights up HEVC.
	hevcSink := avcSink + ",http-get:*:video/mp2t:DLNA.ORG_PN=HEVC_TS_MAIN_HD"
	if !parseSinkProtocolInfo(hevcSink).SupportsCodec(media.CodecHEVC) {
		t.Error("HEVC_TS sink should advertise HEVC")
	}

	// Nothing usable yields no video codecs; the caller substitutes fallbackCaps.
	if got := parseSinkProtocolInfo("garbage,http-get:*:audio/mpeg:*"); len(got.Video) != 0 {
		t.Errorf("unusable sink should yield no video, got %v", got.Video)
	}
	if !fallbackCaps().SupportsCodec(media.CodecH264) {
		t.Error("fallbackCaps must at least support H.264")
	}
}


