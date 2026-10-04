package hls

import (
	"slices"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/source/sourcetest"
)

const multivariantPlaylist = "#EXTM3U\n" +
	"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aud\",URI=\"audio/eng.m3u8\"\n" +
	"#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080,AUDIO=\"aud\"\n" +
	"v/1080.m3u8\n"

func TestAMasterNamesItsRenditionsAndCompanion(t *testing.T) {
	base := sourcetest.URL(t, "https://cdn.example/hls/index.m3u8")
	doc, err := parsePlaylist(multivariantPlaylist, base, base)
	if err != nil {
		t.Fatal(err)
	}
	if !doc.multivariant {
		t.Fatal("the master is not marked multivariant")
	}
	var got []string
	for _, rung := range ladder(doc, nil) {
		got = append(got, rung.URL.String(), rung.AudioURL.String())
	}
	slices.Sort(got)
	if want := []string{"https://cdn.example/hls/audio/eng.m3u8", "https://cdn.example/hls/v/1080.m3u8"}; !slices.Equal(got, want) {
		t.Errorf("names = %q, want %q", got, want)
	}
}

func TestOnlyAPlaylistThatEndedStatesARuntime(t *testing.T) {
	base := sourcetest.URL(t, "https://cdn.example/a/index")
	for _, tc := range []struct {
		name string
		body string
		want time.Duration
	}{
		{"an ended playlist runs its segments' sum", "#EXTM3U\n#EXTINF:6.000,\na.ts\n#EXTINF:4.500,\nb.ts\n#EXT-X-ENDLIST\n", 10500 * time.Millisecond},
		{"a playlist still growing says nothing", "#EXTM3U\n#EXTINF:6.000,\na.ts\n", 0},
		{"a master says nothing", multivariantPlaylist, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parsePlaylist(tc.body, base, base)
			if err != nil {
				t.Fatal(err)
			}
			if got := doc.duration; got != tc.want {
				t.Errorf("runtime = %v, want %v", got, tc.want)
			}
		})
	}
}
