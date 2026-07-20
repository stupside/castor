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

func TestPlanRokuPassthroughVsRemux(t *testing.T) {
	base := PlanInput{
		DeviceType: device.TypeRoku,
		Renderer:   media.Renderer{Containers: []string{media.HLS, media.MP4, media.MKV}},
	}

	// Accepted container: pass through, no transcode.
	in := base
	in.SourceContentType = media.MP4
	if p := BuildPlan(in); p.Transcode != nil {
		t.Errorf("accepted container should pass through (Transcode nil), got %+v", p.Transcode)
	}

	// Unaccepted container: remux to live HLS with the video stream-copied.
	in = base
	in.SourceContentType = media.WebM
	p := BuildPlan(in)
	if p.Transcode == nil {
		t.Fatal("unaccepted container should remux (Transcode non-nil)")
	}
	if p.Transcode.VideoEncoder != nil {
		t.Errorf("roku remux should stream-copy video (nil encoder), got %v", p.Transcode.VideoEncoder)
	}
	if p.OutputContentType != media.HLS {
		t.Errorf("remux output = %q, want %q", p.OutputContentType, media.HLS)
	}
	if p.Transcode.OutputFormat != "hls" {
		t.Errorf("remux output format = %q, want hls", p.Transcode.OutputFormat)
	}
}
