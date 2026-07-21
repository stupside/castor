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
	p, err := BuildPlan(in)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if p.Transcode != nil {
		t.Errorf("accepted container should pass through (Transcode nil), got %+v", p.Transcode)
	}

	// Unaccepted container: remux to mp4 with the video stream-copied.
	in = base
	in.SourceContentType = media.MKV
	p, err = BuildPlan(in)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
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
