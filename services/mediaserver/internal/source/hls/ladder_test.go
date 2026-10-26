package hls

import (
	"net/url"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// pick is the rung resolution chooses from a document under a cap.
func pick(doc document, ceiling media.HeightCap) source.Rendition {
	return source.Origin{Renditions: ladder(doc, nil, "")}.Choose(ceiling, 0, source.ByBitrate)
}

func TestTheRichestRungUnderTheCapIsPicked(t *testing.T) {
	u := func(path string) *url.URL { return &url.URL{Path: path} }
	doc := document{variants: []variant{
		{url: u("/480"), bandwidth: 1_000_000, height: 480, hasVideo: true},
		{url: u("/1080"), bandwidth: 6_000_000, height: 1080, hasVideo: true},
		{url: u("/2160"), bandwidth: 20_000_000, height: 2160, hasVideo: true},
		{url: u("/audio"), bandwidth: 30_000_000},
	}}
	for _, tt := range []struct {
		maxHeight media.HeightCap
		want      string
	}{{1080, "/1080"}, {240, "/480"}} {
		if got := pick(doc, tt.maxHeight).URL.Path; got != tt.want {
			t.Errorf("pick(max=%d) = %q, want %q", tt.maxHeight, got, tt.want)
		}
	}
}

func TestStreamOrderDoesNotStandInForVariantIdentity(t *testing.T) {
	doc := document{multivariant: true, variants: []variant{
		{program: 0, url: &url.URL{Path: "/a"}, hasVideo: true},
		{program: 1, url: &url.URL{Path: "/b"}, hasVideo: true},
	}}
	probe := &media.ProbeInfo{VideoHeights: []int{1080, 360}}
	for _, rung := range ladder(doc, probe) {
		if rung.Height != 0 {
			t.Fatalf("stream order was mistaken for a variant height: %+v", rung)
		}
	}
}
