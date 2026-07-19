package media

import "testing"

// h264 returns an in-envelope 1080p H.264 probe result, so tests can vary one
// field at a time.
func h264() ProbeInfo {
	return ProbeInfo{
		VideoCodec:    CodecH264,
		VideoProfile:  "High",
		VideoLevel:    40,
		VideoWidth:    1920,
		VideoHeight:   1080,
		VideoBitDepth: 8,
	}
}

// samsungLike mirrors the H.264 copy envelope the device package pairs with a
// renderer that advertises H.264, so the matching rules can be exercised here
// without importing the device package.
var samsungLike = Renderer{
	Containers: []string{"video/mp2t", MP4},
	Video: []VideoSupport{
		{Codec: CodecH264, Profiles: []string{"Constrained Baseline", "Baseline", "Main", "High"}, MaxLevel: 42, MaxHeight: 1080},
	},
}

func TestRendererSupportsCodec(t *testing.T) {
	if !samsungLike.SupportsCodec(CodecH264) {
		t.Error("an H.264 renderer should support H.264")
	}
	if samsungLike.SupportsCodec(CodecHEVC) {
		t.Error("an H.264-only renderer must not report HEVC support")
	}
}

func TestRendererCanCopyVideo(t *testing.T) {
	tests := []struct {
		name string
		info ProbeInfo
		want bool
	}{
		{"in-envelope high", h264(), true},
		{"main profile", withProfile(h264(), "Main"), true},
		{"baseline profile", withProfile(h264(), "Baseline"), true},
		{"constrained baseline", withProfile(h264(), "Constrained Baseline"), true},
		{"720p still copies", withHeight(h264(), 720), true},
		{"high 10 rejected", withProfile(h264(), "High 10"), false},
		{"high 4:2:2 rejected", withProfile(h264(), "High 4:2:2"), false},
		{"unknown profile rejected", withProfile(h264(), ""), false},
		{"10-bit rejected", withBitDepth(h264(), 10), false},
		{"hdr rejected", withHDR(h264()), false},
		{"over level rejected", withLevel(h264(), 51), false},
		{"unknown level rejected", withLevel(h264(), 0), false},
		{"above 1080p rejected", withHeight(h264(), 2160), false},
		{"unknown height rejected", withHeight(h264(), 0), false},
		{"hevc rejected", withCodec(h264(), CodecHEVC), false},
		{"zero value rejected", ProbeInfo{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := samsungLike.CanCopyVideo(tt.info); got != tt.want {
				t.Errorf("CanCopyVideo = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRendererAcceptsContainer(t *testing.T) {
	r := Renderer{Containers: []string{HLS, MP4}}
	if !r.AcceptsContainer(HLS) {
		t.Error("HLS is listed; should be accepted")
	}
	if r.AcceptsContainer(MKV) {
		t.Error("MKV is not listed; should not be accepted")
	}
	// A renderer with no video envelope never reports a copy-eligible stream.
	if (Renderer{}).CanCopyVideo(h264()) {
		t.Error("empty renderer should copy nothing")
	}
}

func withProfile(v ProbeInfo, p string) ProbeInfo { v.VideoProfile = p; return v }
func withLevel(v ProbeInfo, l int) ProbeInfo      { v.VideoLevel = l; return v }
func withHeight(v ProbeInfo, h int) ProbeInfo     { v.VideoHeight = h; return v }
func withBitDepth(v ProbeInfo, d int) ProbeInfo   { v.VideoBitDepth = d; return v }
func withCodec(v ProbeInfo, c Codec) ProbeInfo    { v.VideoCodec = c; return v }
func withHDR(v ProbeInfo) ProbeInfo               { v.VideoHDR = true; return v }
