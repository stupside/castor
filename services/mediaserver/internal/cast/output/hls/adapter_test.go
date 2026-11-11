package hls

import (
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

func TestRollingOutputsNeverOutrunReceiverPlayback(t *testing.T) {
	for _, captions := range []*output.Captions{nil, {Cues: &subtitle.Cues{}}} {
		plan, ok := (Adapter{}).Select(media.FormatSupport{Transport: media.TransportHLSWebVTT}, captions)
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
