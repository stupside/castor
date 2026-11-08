package lend

import (
	"net/url"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/execute"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestAPlayStatesWhereItsStreamBegins(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:1/stream.ts")
	for _, start := range []time.Duration{0, 15 * time.Second} {
		play := played(execute.Playback{URL: u, Stream: execute.Served{ContentType: media.MPEGTS}, Start: start})
		if play.GetStart() == nil || play.GetStart().AsDuration() != start {
			t.Errorf("played() start = %v, want %v stated", play.GetStart(), start)
		}
	}
}
