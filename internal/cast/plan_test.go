package cast

import (
	"testing"

	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/media"
)

func TestPlanChromecastPassthroughVsRemux(t *testing.T) {
	base := PlanInput{
		DeviceType: device.TypeChromecast,
		Renderer:   media.Renderer{Containers: []string{media.HLS, media.MP4}},
	}

	// Accepted container: pass through, no transcode.
	in := base
	in.SourceContentType = media.HLS
	if p := BuildPlan(in); p.Transcode != nil {
		t.Errorf("accepted container should pass through (Transcode nil), got %+v", p.Transcode)
	}

	// Unaccepted container: remux to mp4 with the video stream-copied.
	in = base
	in.SourceContentType = media.MKV
	p := BuildPlan(in)
	if p.Transcode == nil {
		t.Fatal("unaccepted container should remux (Transcode non-nil)")
	}
	if p.Transcode.VideoEncoder != nil {
		t.Errorf("chromecast remux should stream-copy video (nil encoder), got %v", p.Transcode.VideoEncoder)
	}
	if p.OutputContentType != media.MP4 {
		t.Errorf("remux output = %q, want %q", p.OutputContentType, media.MP4)
	}
}

// TestDLNACapabilitiesCopyEnvelope checks the DLNA device's capability data and
// the media matching rules wire together end to end: an in-envelope H.264 is
// copy-eligible, a 10-bit one is not. The exhaustive envelope table lives in
// the media package.
func TestDLNACapabilitiesCopyEnvelope(t *testing.T) {
	dlna := device.Capabilities(device.TypeDLNA)
	inEnvelope := media.ProbeInfo{
		VideoCodec:    media.CodecH264,
		VideoProfile:  "High",
		VideoLevel:    40,
		VideoHeight:   1080,
		VideoBitDepth: 8,
	}
	if !dlna.CanCopyVideo(inEnvelope) {
		t.Error("in-envelope 1080p High H.264 should be copy-eligible on DLNA")
	}
	tenBit := inEnvelope
	tenBit.VideoBitDepth = 10
	if dlna.CanCopyVideo(tenBit) {
		t.Error("10-bit H.264 must not be copy-eligible on DLNA")
	}
}
