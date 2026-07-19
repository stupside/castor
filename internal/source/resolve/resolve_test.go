package resolve

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestPickVariant(t *testing.T) {
	u := func(path string) *url.URL { return &url.URL{Path: path} }
	variants := []hlsVariant{
		{URL: u("/480"), Bandwidth: 1_000_000, Height: 480},
		{URL: u("/720"), Bandwidth: 3_000_000, Height: 720},
		{URL: u("/1080"), Bandwidth: 6_000_000, Height: 1080},
		{URL: u("/2160"), Bandwidth: 20_000_000, Height: 2160},
	}
	tests := []struct {
		name      string
		maxHeight int
		want      string
	}{
		{"cap 1080 takes the 1080 variant", 1080, "/1080"},
		{"cap 720 takes the 720 variant", 720, "/720"},
		{"cap 2160 takes the 4K variant", 2160, "/2160"},
		{"cap below every variant takes the smallest", 240, "/480"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickVariant(variants, tt.maxHeight).URL.Path; got != tt.want {
				t.Errorf("pickVariant(max=%d) = %q, want %q", tt.maxHeight, got, tt.want)
			}
		})
	}
}

func TestPickVariantUnknownHeightIsEligible(t *testing.T) {
	u := func(path string) *url.URL { return &url.URL{Path: path} }
	// A variant with no RESOLUTION tag (Height 0) is trusted and wins on bandwidth
	// rather than being excluded by the cap.
	variants := []hlsVariant{
		{URL: u("/tagged"), Bandwidth: 1_000_000, Height: 1080},
		{URL: u("/untagged"), Bandwidth: 5_000_000, Height: 0},
	}
	if got := pickVariant(variants, 1080).URL.Path; got != "/untagged" {
		t.Errorf("pickVariant = %q, want /untagged (unknown height, higher bandwidth)", got)
	}
}

func TestParsePlaylistResolution(t *testing.T) {
	master := "#EXTM3U\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=854x480\n480.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080\n1080.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=20000000,RESOLUTION=3840x2160\n2160.m3u8\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, master)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL + "/master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	variants, err := parsePlaylist(context.Background(), 5*time.Second, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{480, 1080, 2160}
	if len(variants) != len(want) {
		t.Fatalf("got %d variants, want %d", len(variants), len(want))
	}
	for i, v := range variants {
		if v.Height != want[i] {
			t.Errorf("variant %d height = %d, want %d (RESOLUTION not parsed)", i, v.Height, want[i])
		}
	}
	// The cap steers selection end to end.
	if got := pickVariant(variants, 1080).URL.Path; got != "/1080.m3u8" {
		t.Errorf("pickVariant(1080) = %q, want /1080.m3u8", got)
	}
}
