package source

import (
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestDeclaresVideoReadsTheSampleEntry(t *testing.T) {
	for _, tc := range []struct {
		codecs string
		want   bool
	}{
		{"avc1.640028,mp4a.40.2", true},
		{"mp4a.40.2, HVC1.1.6.L120.90", true},
		{"mp4a.40.2", false},
		{"avc1x.640028", false},
	} {
		if got := DeclaresVideo(tc.codecs); got != tc.want {
			t.Errorf("DeclaresVideo(%q) = %v, want %v", tc.codecs, got, tc.want)
		}
	}
}

func TestDeclaredEnvelopeReadsBothH264Spellings(t *testing.T) {
	for _, tc := range []struct {
		codecs string
		want   media.Profile
	}{
		{"avc1.640028,mp4a.40.2", media.ProfileHigh},
		{"avc1.42c01e,mp4a.40.2", media.ProfileConstrainedBaseline},
		{"avc1.100.30,mp4a.40.2", media.ProfileHigh},
		{"avc1.66.30,mp4a.40.2", media.ProfileBaseline},
	} {
		got := DeclaredEnvelope(tc.codecs, 360)
		if got == nil || got.VideoCodec != media.CodecH264 || got.VideoProfile != tc.want {
			t.Errorf("DeclaredEnvelope(%q) = %+v, want h264 %s", tc.codecs, got, tc.want)
		}
	}
	for _, codecs := range []string{"avc1.100.x1", "avc1.1000.30", "avc1.64002"} {
		if got := DeclaredEnvelope(codecs, 360); got != nil {
			t.Errorf("DeclaredEnvelope(%q) = %+v, want nil for a malformed entry", codecs, got)
		}
	}
}

func TestADeclaredLevelCannotBypassTheDevicesDecoderCeiling(t *testing.T) {
	device := media.Capabilities{Video: []media.VideoSupport{{Codec: media.CodecH264, MaxLevel: 42}}}
	for _, codecs := range []string{"avc1.640033", "avc1.100.51"} {
		picture := DeclaredEnvelope(codecs, 1080)
		if picture == nil {
			t.Fatalf("no declaration for %s", codecs)
		}
		if device.CanCopyVideo(*picture) {
			t.Errorf("level 5.1 from %s was accepted by a level 4.2 decoder", codecs)
		}
	}
}
