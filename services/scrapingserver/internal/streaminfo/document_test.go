package streaminfo

import (
	"net/url"
	"slices"
	"testing"
	"time"
)

func TestCaptureDocumentsNeedNoPlaybackReader(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/program/master.m3u8")
	for _, tc := range []struct {
		name, body string
		ladder     Ladder
		runtime    time.Duration
		refs       []string
	}{
		{"master", "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,URI=\"audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=123\nvideo.m3u8\n", LadderMultivariant, 0, []string{"audio.m3u8", "video.m3u8"}},
		{"closed ad", "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:6.5,\nseg.m4s\n#EXT-X-ENDLIST\n", LadderSole, 6500 * time.Millisecond, []string{"init.mp4", "seg.m4s"}},
		{"live rendition", "#EXTM3U\n#EXTINF:6,\nseg.ts\n", LadderSole, 0, []string{"seg.ts"}},
		{"DASH", `<MPD><BaseURL>movie.mp4</BaseURL><Period><AdaptationSet><Representation><SegmentList><Initialization sourceURL="init.mp4"/><SegmentURL media="part.m4s"/></SegmentList></Representation><Representation><SegmentTemplate media="part-$Number$.m4s" initialization="init2.mp4"/></Representation></AdaptationSet></Period></MPD>`, LadderMultivariant, 0, []string{"movie.mp4", "init.mp4", "part.m4s", "init2.mp4"}},
		{"opaque", "<html>video</html>", LadderUnknown, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := ParseDocument(tc.body, base)
			if doc.Ladder != tc.ladder || doc.Runtime != tc.runtime {
				t.Errorf("evidence: %+v", doc)
			}
			var refs []string
			for _, ref := range doc.Names {
				refs = append(refs, ref.String())
			}
			var want []string
			for _, ref := range tc.refs {
				u, _ := base.Parse(ref)
				want = append(want, u.String())
			}
			if !slices.Equal(refs, want) {
				t.Errorf("references: %v, want %v", refs, want)
			}
		})
	}
}
