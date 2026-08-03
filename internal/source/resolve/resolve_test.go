package resolve

import (
	"net/url"
	"testing"

	"github.com/stupside/castor/internal/media"
)

func TestPickVariant(t *testing.T) {
	u := func(path string) *url.URL { return &url.URL{Path: path} }
	variants := []hlsVariant{
		{URL: u("/480"), Bandwidth: 1_000_000, Height: 480, HasVideo: true},
		{URL: u("/720"), Bandwidth: 3_000_000, Height: 720, HasVideo: true},
		{URL: u("/1080"), Bandwidth: 6_000_000, Height: 1080, HasVideo: true},
		{URL: u("/2160"), Bandwidth: 20_000_000, Height: 2160, HasVideo: true},
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
		{URL: u("/tagged"), Bandwidth: 1_000_000, Height: 1080, HasVideo: true},
		{URL: u("/untagged"), Bandwidth: 5_000_000, Height: 0, HasVideo: true},
	}
	if got := pickVariant(variants, 1080).URL.Path; got != "/untagged" {
		t.Errorf("pickVariant = %q, want /untagged (unknown height, higher bandwidth)", got)
	}
}

func TestParsePlaylist(t *testing.T) {
	body := "#EXTM3U\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=854x480\n480.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080\n1080.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=20000000,RESOLUTION=3840x2160\n2160.m3u8\n"
	u, err := url.Parse("http://example.com/master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	master, err := parsePlaylist(body, u)
	if err != nil {
		t.Fatal(err)
	}
	if master.Live {
		t.Error("master playlist should not report live")
	}
	want := []int{480, 1080, 2160}
	if len(master.Variants) != len(want) {
		t.Fatalf("got %d variants, want %d", len(master.Variants), len(want))
	}
	for i, v := range master.Variants {
		if v.Height != want[i] {
			t.Errorf("variant %d height = %d, want %d (RESOLUTION not parsed)", i, v.Height, want[i])
		}
	}
	// The cap steers selection end to end.
	if got := pickVariant(master.Variants, 1080).URL.Path; got != "/1080.m3u8" {
		t.Errorf("pickVariant(1080) = %q, want /1080.m3u8", got)
	}
}

// TestParsePlaylistDemuxedMaster is the shape that silently loses audio: the
// variants carry video only and the audio is a rendition of its own. Resolving
// the master must keep the pair together, or the cast plays silence and the
// puller dies mapping an audio track that is not in the variant it was handed.
func TestParsePlaylistDemuxedMaster(t *testing.T) {
	body := "#EXTM3U\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",DEFAULT=YES,URI="audio/eng.m3u8"` + "\n" +
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English",URI="subs/eng.m3u8"` + "\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=854x480,CODECS="avc1.4d401f,mp4a.40.2",AUDIO="aud"` + "\n480.m3u8\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="aud"` + "\n1080.m3u8\n"
	u, err := url.Parse("http://example.com/master.m3u8")
	if err != nil {
		t.Fatal(err)
	}

	master, err := parsePlaylist(body, u)
	if err != nil {
		t.Fatal(err)
	}
	if len(master.Variants) != 2 {
		t.Fatalf("got %d variants, want 2 (a CODECS list holds the same comma that separates attributes)", len(master.Variants))
	}
	variant := pickVariant(master.Variants, 1080)
	if got := variant.URL.Path; got != "/1080.m3u8" {
		t.Errorf("pickVariant = %q, want /1080.m3u8", got)
	}
	audio := master.AudioFor(variant)
	if audio == nil {
		t.Fatal("the chosen variant's audio rendition was dropped: the cast would have no sound")
	}
	if got := audio.Path; got != "/audio/eng.m3u8" {
		t.Errorf("audio rendition = %q, want /audio/eng.m3u8", got)
	}
	// Only audio is read as a second input today; a subtitle rendition is not.
	if _, ok := master.Audio["subs"]; ok {
		t.Error("a SUBTITLES rendition must not register as an audio group")
	}
}

// TestParsePlaylistMuxedRenditionStaysSingleInput guards the other half: an
// EXT-X-MEDIA entry with no URI means the audio is inside the variant already,
// so nothing extra must be read.
func TestParsePlaylistMuxedRenditionStaysSingleInput(t *testing.T) {
	body := "#EXTM3U\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",DEFAULT=YES` + "\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=854x480,AUDIO="aud"` + "\n480.m3u8\n"
	u, _ := url.Parse("http://example.com/master.m3u8")

	master, err := parsePlaylist(body, u)
	if err != nil {
		t.Fatal(err)
	}
	if got := master.AudioFor(pickVariant(master.Variants, 1080)); got != nil {
		t.Errorf("a rendition without a URI is muxed into the variant; got a second input %q", got)
	}
}

// TestParsePlaylistRenditionAfterVariants covers declaration order, which a
// master is free to choose: groups declared under the variants that use them
// must still be found. The decoder hangs renditions off a neighbouring variant,
// so the group map is built from every variant rather than from the first.
func TestParsePlaylistRenditionAfterVariants(t *testing.T) {
	body := "#EXTM3U\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="aud"` + "\n1080.m3u8\n" +
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",DEFAULT=YES,URI="audio/eng.m3u8"` + "\n"
	u, _ := url.Parse("http://example.com/master.m3u8")

	master, err := parsePlaylist(body, u)
	if err != nil {
		t.Fatal(err)
	}
	audio := master.AudioFor(pickVariant(master.Variants, 1080))
	if audio == nil {
		t.Fatal("a rendition declared after its variant was dropped: the cast would have no sound")
	}
	if got := audio.Path; got != "/audio/eng.m3u8" {
		t.Errorf("audio rendition = %q, want /audio/eng.m3u8", got)
	}
}

// TestPickVariantSkipsAudioOnly covers the inverse failure: a master that lists
// its audio rendition as a variant of its own. It has no RESOLUTION to exclude it
// by the cap and can out-bandwidth the video variants, so on bandwidth alone it
// would win and the cast would die mapping a video track that is not there.
func TestPickVariantSkipsAudioOnly(t *testing.T) {
	body := "#EXTM3U\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=560000,RESOLUTION=320x240,CODECS="avc1.42c00c,mp4a.40.2",AUDIO="aud"` + "\n" +
		"video.m3u8\n" +
		`#EXT-X-STREAM-INF:BANDWIDTH=1400000,CODECS="mp4a.40.2",AUDIO="aud"` + "\naudio.m3u8\n"
	u, _ := url.Parse("http://example.com/master.m3u8")

	master, err := parsePlaylist(body, u)
	if err != nil {
		t.Fatal(err)
	}
	if got := pickVariant(master.Variants, 1080).URL.Path; got != "/video.m3u8" {
		t.Errorf("pickVariant = %q, want /video.m3u8 (an audio-only variant is not castable)", got)
	}
}

func TestBestCandidate(t *testing.T) {
	direct := func(path string, height int, bw int64) candidate {
		return candidate{stream: &media.Stream{URL: &url.URL{Path: path}, Bandwidth: bw, ContentType: media.MP4}, height: height}
	}
	hls := func(path string, height int, bw int64) candidate {
		return candidate{stream: &media.Stream{URL: &url.URL{Path: path}, Bandwidth: bw, ContentType: media.HLS}, height: height}
	}

	tests := []struct {
		name      string
		pool      []candidate
		maxHeight int
		want      string
	}{
		{
			name:      "in-cap direct beats over-cap direct despite lower bitrate",
			pool:      []candidate{direct("/4k", 2160, 20_000_000), direct("/1080", 1080, 6_000_000)},
			maxHeight: 1080,
			want:      "/1080",
		},
		{
			name:      "HLS master is exempt from the cap and wins on bandwidth",
			pool:      []candidate{hls("/master", 2160, 20_000_000), direct("/1080", 1080, 6_000_000)},
			maxHeight: 1080,
			want:      "/master",
		},
		{
			name:      "all over cap falls back to highest bandwidth",
			pool:      []candidate{direct("/4k", 2160, 20_000_000), direct("/1440", 1440, 10_000_000)},
			maxHeight: 1080,
			want:      "/4k",
		},
		{
			name:      "unknown height is eligible",
			pool:      []candidate{direct("/unknown", 0, 20_000_000), direct("/1080", 1080, 6_000_000)},
			maxHeight: 1080,
			want:      "/unknown",
		},
		{
			name:      "tied bandwidth (unprobeable HLS master bit_rate) falls back to tallest height",
			pool:      []candidate{hls("/290", 290, 1), hls("/1808", 1808, 1), hls("/580", 580, 1)},
			maxHeight: 2160,
			want:      "/1808",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bestCandidate(tt.pool, tt.maxHeight).stream.URL.Path; got != tt.want {
				t.Errorf("bestCandidate = %q, want %q", got, tt.want)
			}
		})
	}
}
