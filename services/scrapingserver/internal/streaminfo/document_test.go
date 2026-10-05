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

func TestDocumentMarkersInErrorPagesAreNotEvidence(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/master.m3u8")
	for _, body := range []string{
		"<html>missing playlist #EXTM3U\nother.m3u8</html>",
		"<html><MPD>denied</MPD></html>",
		"<MPD><BaseURL>movie.mp4</BaseURL>",
	} {
		if doc := ParseDocument(body, base); doc.Ladder != LadderUnknown || len(doc.Names) != 0 {
			t.Errorf("error body accepted as document: %+v", doc)
		}
	}
}

func TestDASHReferencesUseInheritedBaseURLs(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/program/manifest.mpd")
	body := `<d:MPD xmlns:d="urn:mpeg:dash:schema:mpd:2011"><d:BaseURL>../assets/</d:BaseURL><d:Period><d:BaseURL>feature/</d:BaseURL><d:AdaptationSet><d:Representation><d:BaseURL>video/</d:BaseURL><d:SegmentList><d:Initialization sourceURL="init.mp4"/><d:SegmentURL media="seg.mp4"/></d:SegmentList></d:Representation></d:AdaptationSet></d:Period></d:MPD>`
	doc := ParseDocument(body, base)
	if doc.Ladder != LadderMultivariant {
		t.Fatalf("namespaced MPD not recognized: %+v", doc)
	}
	var names []string
	for _, u := range doc.Names {
		names = append(names, u.String())
	}
	for _, want := range []string{"https://cdn.example/assets/feature/video/init.mp4", "https://cdn.example/assets/feature/video/seg.mp4"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing inherited reference %s in %v", want, names)
		}
	}
}

func TestDASHReferencesUseAlternativeBaseURLsWithoutMixingSiblings(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/program/manifest.mpd")
	doc := ParseDocument(`<MPD><BaseURL>assets/</BaseURL><BaseURL>backup/</BaseURL><Period><AdaptationSet><Representation><BaseURL>video/</BaseURL><SegmentList><SegmentURL media="seg.mp4"/></SegmentList></Representation><Representation><BaseURL>audio/</BaseURL><SegmentList><SegmentURL media="seg.mp4"/></SegmentList></Representation></AdaptationSet></Period></MPD>`, base)
	var names []string
	for _, u := range doc.Names {
		names = append(names, u.String())
	}
	for _, directory := range []string{"assets/video", "backup/video", "assets/audio", "backup/audio"} {
		want := "https://cdn.example/program/" + directory + "/seg.mp4"
		if !slices.Contains(names, want) {
			t.Errorf("missing alternative/sibling reference %s in %v", want, names)
		}
	}
}

func TestInvalidPlaylistDurationsDoNotInventShortRuntime(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/master.m3u8")
	for _, value := range []string{"-1", "NaN", "+Inf", "9e30", "invalid"} {
		doc := ParseDocument("#EXTM3U\n#EXTINF:6,\nfirst.ts\n#EXTINF:"+value+",\nlast.ts\n#EXT-X-ENDLIST\n", base)
		if doc.Runtime != 0 {
			t.Errorf("duration %q invented runtime %v", value, doc.Runtime)
		}
	}
}
