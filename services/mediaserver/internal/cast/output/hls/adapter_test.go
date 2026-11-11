package hls

import (
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

func TestRollingOutputsNeverOutrunReceiverPlayback(t *testing.T) {
	for _, captions := range []*output.Captions{nil, {Cues: &subtitle.Cues{}}} {
		plan, ok := (Adapter{}).Select(media.FormatSupport{Transport: media.TransportHLSWebVTT}, captions, 0)
		if !ok {
			t.Fatal("supported envelope refused")
		}
		for _, burn := range []bool{false, true} {
			pace := plan.Pace(burn)
			if pace.Realtime != 1 || pace.Catchup > 1 {
				t.Fatalf("rolling output can delete unwatched media: %+v", pace)
			}
		}
	}
}

func TestFiniteHLSKeepsEarlierSegmentsAvailableForSeeking(t *testing.T) {
	plan, ok := (Adapter{}).Select(media.FormatSupport{Transport: media.TransportHLS}, nil, 2*time.Hour)
	if !ok {
		t.Fatal("finite HLS refused")
	}
	args := strings.Join(plan.EncoderFormat().Tuning.Args, " ")
	if !strings.Contains(args, "-hls_playlist_type event") || !strings.Contains(args, "-hls_list_size 0") || strings.Contains(args, "delete_segments") {
		t.Fatalf("finite video loses its seekable history: %s", args)
	}
	if !strings.Contains(args, "-master_pl_name master.m3u8") {
		t.Fatalf("finite native stream must declare its actual codecs: %s", args)
	}
	if pace := plan.Pace(false); pace.Realtime != 0 {
		t.Fatalf("finite remux is paced as a rolling live window: %+v", pace)
	}
}

func TestOnlyGeneratedCaptionsCutTheEncodeOnTheClock(t *testing.T) {
	for _, tt := range []struct {
		captions *output.Captions
		want     bool
	}{{nil, false}, {&output.Captions{Cues: &subtitle.Cues{}}, true}} {
		plan, ok := (Adapter{}).Select(media.FormatSupport{Transport: media.TransportHLSWebVTT}, tt.captions, 0)
		if !ok {
			t.Fatal("supported envelope refused")
		}
		if got := plan.Format().ClockCuts; got != tt.want {
			t.Errorf("captions %t: clock cuts %t, want %t", tt.captions != nil, got, tt.want)
		}
	}
}
