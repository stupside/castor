package source_test

import (
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/dash"
	"github.com/stupside/castor/services/mediaserver/internal/source/hls"
	"github.com/stupside/castor/services/mediaserver/internal/source/sourcetest"
)

func TestIdentifyKeepsNamedFormatsAndSniffsIncompleteManifests(t *testing.T) {
	formats := source.Formats{hls.Format{}, dash.Format{}}
	for _, tc := range []struct {
		raw, body, want string
	}{
		{"https://cdn.example/MASTER.M3U8", "", media.HLS},
		{"https://cdn.example/manifest.mpd", "", media.DASH},
		{"https://cdn.example/movie.mp4", "", media.MP4},
		{"https://cdn.example/index", "#EXTM3U\n#EXT-X-STREAM-INF:", media.HLS},
		{"https://cdn.example/index", `<?xml version="1.0"?><MPD type="static"><Period>`, media.DASH},
		{"https://cdn.example/index", "<html>not found</html>", ""},
	} {
		client := &sourcetest.Documents{ByPath: map[string]string{"/index": tc.body}}
		if got := formats.Identify(t.Context(), client, sourcetest.URL(t, tc.raw)); got != tc.want {
			t.Errorf("Identify(%s) = %q, want %q", tc.raw, got, tc.want)
		}
		if asked := client.Asked(); tc.body == "" && len(asked) != 0 {
			t.Errorf("a named format fetched %v, want no request", asked)
		}
	}
	if got := formats.Identify(t.Context(), &sourcetest.Document{Status: 403}, sourcetest.URL(t, "https://cdn.example/index")); got != "" {
		t.Errorf("a refused sniff identified %q, want unknown", got)
	}
}

func TestALinkIsNamedByTheFormatThatDeclaresItsName(t *testing.T) {
	formats := source.Formats{hls.Format{}, dash.Format{}}
	for _, tc := range []struct {
		raw, mime, want string
	}{
		{"https://cdn.example/MASTER.M3U8?token=x", "", media.HLS},
		{"https://cdn.example/index", "Application/VND.Apple.MPEGURL", media.HLS},
		// The name the link spells wins over the type the server claims.
		{"https://cdn.example/master.m3u8", "video/mp4", media.HLS},
		{"https://cdn.example/player/embed", "text/html", ""},
	} {
		if got := formats.ContentTypeOf(sourcetest.URL(t, tc.raw), tc.mime); got != tc.want {
			t.Errorf("ContentTypeOf(%s, %q) = %q, want %q", tc.raw, tc.mime, got, tc.want)
		}
	}
}
