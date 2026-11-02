package execute

import (
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

func TestNativePlaybackCannotAdaptToALowerRendition(t *testing.T) {
	p := &preparation{
		PreserveSource: true, MaxHeight: 2147483647,
		Resolver: &source.Resolver{},
		attempt: recovery.Attempt{
			Stream: &source.Stream{}, Rendition: source.Rendition{Bitrate: 8_000_000, Height: 2160},
			Origin: source.Origin{Renditions: []source.Rendition{{Bitrate: 8_000_000, Height: 2160}, {Bitrate: 2_000_000, Height: 720}}},
		},
	}
	if got := p.rungs(); got != nil {
		t.Fatalf("native cast offered %v for automatic quality changes", got)
	}
	p.PreserveSource = false
	if got := p.rungs(); len(got) != 2 {
		t.Fatalf("optimized cast lost its adaptive renditions: %v", got)
	}
}
