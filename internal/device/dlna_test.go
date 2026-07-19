package device

import (
	"net/url"
	"slices"
	"testing"

	"github.com/huin/goupnp"

	"github.com/stupside/castor/internal/media"
)

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
	if !hasCodec(caps, media.CodecH264) {
		t.Error("AVC sink should advertise H.264")
	}
	if hasCodec(caps, media.CodecHEVC) {
		t.Error("AVC-only sink must not advertise HEVC")
	}
	if !slices.Contains(caps.Containers, "video/mp2t") {
		t.Errorf("expected video/mp2t container, got %v", caps.Containers)
	}

	// The discovered H.264 envelope copies an in-envelope stream, not a 10-bit one.
	inEnvelope := media.ProbeInfo{VideoCodec: media.CodecH264, VideoProfile: "High", VideoLevel: 40, VideoHeight: 1080, VideoBitDepth: 8}
	if !caps.CanCopyVideo(inEnvelope) {
		t.Error("in-envelope 1080p High H.264 should be copy-eligible")
	}
	tenBit := inEnvelope
	tenBit.VideoBitDepth = 10
	if caps.CanCopyVideo(tenBit) {
		t.Error("10-bit H.264 must not be copy-eligible")
	}

	// A renderer advertising an HEVC TS profile lights up HEVC.
	hevcSink := avcSink + ",http-get:*:video/mp2t:DLNA.ORG_PN=HEVC_TS_MAIN_HD"
	if !hasCodec(parseSinkProtocolInfo(hevcSink), media.CodecHEVC) {
		t.Error("HEVC_TS sink should advertise HEVC")
	}

	// Resolution is discovered from the PN class: a UHD-advertising renderer
	// copies 4K, an HD-only one does not.
	fourK := media.ProbeInfo{VideoCodec: media.CodecHEVC, VideoProfile: "Main", VideoLevel: 153, VideoHeight: 2160, VideoBitDepth: 8}
	if !parseSinkProtocolInfo("http-get:*:video/mp2t:DLNA.ORG_PN=HEVC_TS_MAIN_UHD").CanCopyVideo(fourK) {
		t.Error("a UHD-advertising renderer should copy 4K HEVC")
	}
	if parseSinkProtocolInfo("http-get:*:video/mp2t:DLNA.ORG_PN=HEVC_TS_MAIN_HD").CanCopyVideo(fourK) {
		t.Error("an HD-only renderer must not copy 4K")
	}

	// Nothing usable yields no video codecs; the caller substitutes fallbackCaps.
	if got := parseSinkProtocolInfo("garbage,http-get:*:audio/mpeg:*"); len(got.Video) != 0 {
		t.Errorf("unusable sink should yield no video, got %v", got.Video)
	}
	if !hasCodec(fallbackCaps(), media.CodecH264) {
		t.Error("fallbackCaps must at least support H.264")
	}
}

func hasCodec(r media.Renderer, c media.Codec) bool {
	return slices.ContainsFunc(r.Video, func(v media.VideoSupport) bool { return v.Codec == c })
}
