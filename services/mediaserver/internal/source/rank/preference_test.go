package rank

import (
	"net/url"
	"slices"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

func TestRankedPicksTheBestCandidate(t *testing.T) {
	at := func(contentType, path string, height int, bw int64) *source.Stream {
		return &source.Stream{URL: &url.URL{Path: path}, ContentType: contentType, Probe: &media.ProbeInfo{VideoHeight: height, BitRate: bw}}
	}
	direct := func(path string, height int, bw int64) *source.Stream { return at(media.MP4, path, height, bw) }
	hls := func(path string, height int, bw int64) *source.Stream { return at(media.HLS, path, height, bw) }
	withLadder := func(c *source.Stream, l source.Ladder) *source.Stream { c.Ladder = l; return c }
	unmeasured := &source.Stream{URL: &url.URL{Path: "/dead"}, ContentType: media.MP4, LastResort: true}

	for _, tt := range []struct {
		name      string
		pool      []*source.Stream
		maxHeight media.HeightCap
		want      string
	}{
		{"in-cap beats over-cap despite lower bitrate", []*source.Stream{direct("/4k", 2160, 20_000_000), direct("/1080", 1080, 6_000_000)}, 1080, "/1080"},
		{"a master is exempt from the cap", []*source.Stream{withLadder(hls("/master", 2160, 20_000_000), source.LadderMultivariant), direct("/1080", 1080, 6_000_000)}, 1080, "/master"},
		{"a sole rendition is bound by the cap", []*source.Stream{withLadder(hls("/v2160", 2160, 20_000_000), source.LadderSole), direct("/1080", 1080, 6_000_000)}, 1080, "/1080"},
		{"an unread playlist keeps the exemption", []*source.Stream{hls("/unread", 2160, 20_000_000), direct("/1080", 1080, 6_000_000)}, 1080, "/unread"},
		{"all over cap falls back to the tallest", []*source.Stream{direct("/4k", 2160, 20_000_000), direct("/1440", 1440, 10_000_000)}, 1080, "/4k"},
		{"height beats bitrate", []*source.Stream{hls("/recording", 800, 462), hls("/release", 1600, 0)}, 1080, "/release"},
		{"equal heights fall to the higher bitrate", []*source.Stream{hls("/thin", 1080, 800_000), hls("/rich", 1080, 6_000_000)}, 1080, "/rich"},
		{"a last resort loses to anything measured", []*source.Stream{unmeasured, direct("/4k", 2160, 20_000_000)}, 1080, "/4k"},
		{"a confirmed ladder outranks a document with none", []*source.Stream{withLadder(hls("/master", 0, 0), source.LadderMultivariant), hls("/variant", 1080, 6_000_000)}, 1080, "/master"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ranked(tt.pool, tt.maxHeight, nil)[0].URL.Path; got != tt.want {
				t.Errorf("ranked head = %q, want %q", got, tt.want)
			}
		})
	}
}

// The comparator once was cyclic; every arrival order must rank the same.
func TestRankedIsIndependentOfArrivalOrder(t *testing.T) {
	candidate := func(path string, height int, bandwidth int64) *source.Stream {
		return &source.Stream{URL: &url.URL{Path: path}, ContentType: media.HLS, Probe: &media.ProbeInfo{VideoHeight: height, BitRate: bandwidth}}
	}
	items := []*source.Stream{candidate("/1080", 1080, 0), candidate("/unknown", 0, 500_000), candidate("/720", 720, 800_000)}
	want := []string{"/1080", "/720", "/unknown"}
	for _, p := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		order := ranked([]*source.Stream{items[p[0]], items[p[1]], items[p[2]]}, 1080, nil)
		if got := []string{order[0].URL.Path, order[1].URL.Path, order[2].URL.Path}; !slices.Equal(got, want) {
			t.Errorf("arrival %v ranked as %v, want %v", p, got, want)
		}
	}
}

func TestTheLanguageAskedForOutranksTheQuality(t *testing.T) {
	speaking := func(path string, height int, languages ...string) *source.Stream {
		info := &media.ProbeInfo{VideoHeight: height}
		for _, l := range languages {
			info.AudioTracks = append(info.AudioTracks, media.AudioTrack{Language: l})
		}
		return &source.Stream{URL: &url.URL{Path: path}, ContentType: media.MP4, Probe: info}
	}
	for _, tt := range []struct {
		name      string
		pool      []*source.Stream
		languages []string
		want      string
	}{
		{"a worse picture in the language beats a better one in another", []*source.Stream{speaking("/en1080", 1080, "en"), speaking("/fr720", 720, "fr")}, []string{"fr"}, "/fr720"},
		{"the earlier language wins", []*source.Stream{speaking("/en", 1080, "en"), speaking("/fr", 480, "fr")}, []string{"fr", "en"}, "/fr"},
		{"an undeclared language does not beat a declared other one on the picture", []*source.Stream{speaking("/en", 1080, "en"), speaking("/unknown", 480)}, []string{"fr"}, "/en"},
		{"a declared match beats an undeclared one", []*source.Stream{speaking("/unknown", 1080), speaking("/fr", 480, "fr")}, []string{"fr"}, "/fr"},
		{"the same language falls to the picture", []*source.Stream{speaking("/fr480", 480, "fr"), speaking("/fr1080", 1080, "fr")}, []string{"fr"}, "/fr1080"},
		{"no language asked leaves the picture to decide", []*source.Stream{speaking("/en1080", 1080, "en"), speaking("/fr720", 720, "fr")}, nil, "/en1080"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ranked(tt.pool, 1080, tt.languages)[0].URL.Path; got != tt.want {
				t.Errorf("ranked first %s, want %s", got, tt.want)
			}
		})
	}
}
