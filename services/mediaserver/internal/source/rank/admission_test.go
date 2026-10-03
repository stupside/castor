package rank

import (
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// probed is the candidate as measureAll leaves it; a nil info is a link nobody opened.
func probed(c *source.Stream, reach media.Reach, info *media.ProbeInfo) measured {
	c.Probe = info
	return measured{Stream: c, reach: reach}
}

func TestAdmissions(t *testing.T) {
	hlsAt := func(raw string) *source.Stream { return candidateAt(t, media.HLS, raw) }
	hls := func(raw string) measured { return measured{Stream: hlsAt(raw)} }
	slideshow := &media.ProbeInfo{
		BitRate: 50_000_000, Duration: 2 * time.Hour, ContentType: media.HLS,
		VideoCodec: media.CodecMJPEG, VideoHeight: 360, VideoHeights: []int{360}, AudioCodec: media.CodecAAC,
	}
	initSegment := playable(9_000_000, 1080, 0)
	initSegment.ContentType = media.MP4

	for _, tc := range []struct {
		name           string
		c              measured
		wantReason     reason
		wantAdmit      bool
		wantLastResort bool
	}{
		{"the origin refused it", probed(hlsAt("http://a.example/spent.m3u8"), media.ReachRefused, nil), reasonRefused, false, false},
		{"unmeasured and unrefused is a last resort", hls("http://a.example/unset.m3u8"), reasonUnproven, true, true},
		{"a browser handle", hls("blob:https://play.example/17147e13"), reasonBrowserInternal, false, false},
		{"a slideshow is not a program", probed(hlsAt("http://a.example/s.m3u8"), media.ReachOpened, slideshow), reasonNoProgram, false, false},
		{"a short runtime is a last resort", probed(hlsAt("http://a.example/ad.m3u8"), media.ReachOpened, playable(9_000_000, 720, 90*time.Second)), reasonTooShort, true, true},
		{"an unstated runtime is not a short one", probed(hlsAt("http://a.example/live.m3u8"), media.ReachOpened, playable(3_000_000, 1080, 0)), reasonCastable, true, false},
		{"an MP4 with no runtime is a fragment header", probed(hlsAt("http://a.example/init.mp4"), media.ReachOpened, initSegment), reasonFragmentHeader, true, true},
		{"measured and castable", probed(hlsAt("http://a.example/f.m3u8"), media.ReachOpened, playable(3_000_000, 1080, 2*time.Hour)), reasonCastable, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := admit(tc.c)
			if got.reason != tc.wantReason || got.admit != tc.wantAdmit || got.lastResort != tc.wantLastResort {
				t.Errorf("admit = (%q, %v, last resort %v), want (%q, %v, %v)",
					got.reason, got.admit, got.lastResort, tc.wantReason, tc.wantAdmit, tc.wantLastResort)
			}
		})
	}
}
