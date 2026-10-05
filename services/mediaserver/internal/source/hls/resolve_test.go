package hls

import (
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/sourcetest"
)

// newTestResolver wires resolution to this format alone, under a 1080 cap.
func newTestResolver(client source.Client) *source.Resolver {
	return source.NewResolver(client, 1080, source.Formats{Format{}})
}

const mediaPlaylist = "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4.0,\nseg0.ts\n#EXT-X-ENDLIST\n"

func resolve(t *testing.T, client source.Client, stream *source.Stream) source.Resolution {
	t.Helper()
	resolved, err := newTestResolver(client).Resolve(t.Context(), stream, source.Rendition{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return resolved
}

func hlsAt(t *testing.T, raw string) *source.Stream {
	t.Helper()
	return &source.Stream{URL: sourcetest.URL(t, raw), ContentType: media.HLS}
}

func TestResolveNarrowsAMasterToTheRungUnderTheCap(t *testing.T) {
	playlists := sourcetest.Testdata(t, "testdata")
	resolved := resolve(t, playlists, hlsAt(t, "http://a.example/master_ladder.m3u8"))
	origin, chosen := resolved.Origin, resolved.Rendition

	if got := sourcetest.PrimaryInput(t, resolved.Program).URL.String(); got != "http://a.example/ladder_1080.m3u8" {
		t.Errorf("chosen rendition = %s, want the 1080 rung under the 1080 cap", got)
	}
	if chosen.Height != 1080 || chosen.Bitrate != 6_200_000 {
		t.Errorf("chosen rung = %+v, want 1080p at the declared 6200000 bit/s", chosen)
	}
	if len(origin.Renditions) != 3 || origin.Sole() {
		t.Errorf("renditions = %v, want the whole three-rung ladder", origin.Renditions)
	}
	if origin.Framing != media.FramingOutOfBand || origin.Duration != 18*time.Second || origin.Live || origin.Protection != "" {
		t.Errorf("origin = %+v, want the chosen rendition's facts: out-of-band, 18s, VOD, clear", origin)
	}
	if got := playlists.Asked(); !slices.Equal(got, []string{"/master_ladder.m3u8", "/ladder_1080.m3u8"}) {
		t.Errorf("fetched %v, want the master then the chosen rendition alone", got)
	}
}

func TestResolveKeepsTheStreamWhenThePlaylistIsRefused(t *testing.T) {
	const raw = "http://a.example/spent.m3u8"
	resolved := resolve(t, &sourcetest.Document{Status: http.StatusForbidden}, hlsAt(t, raw))
	if got := sourcetest.PrimaryInput(t, resolved.Program).URL.String(); got != raw {
		t.Errorf("URL = %s, want the original %s", got, raw)
	}
	if resolved.Rendition.URL != nil {
		t.Errorf("chosen rung = %+v, want none: the source is attempted whole", resolved.Rendition)
	}
}

// A published shape that once panicked: a master listing only I-frame playlists.
func TestResolveSurvivesAMasterOfferingNothingCastable(t *testing.T) {
	resolved := resolve(t, sourcetest.Testdata(t, "testdata"), hlsAt(t, "http://a.example/master_iframe_only.m3u8"))
	if got := sourcetest.PrimaryInput(t, resolved.Program).URL.String(); got != "http://a.example/master_iframe_only.m3u8" {
		t.Errorf("URL = %s, want the original: there was no rendition to narrow to", got)
	}
	if len(resolved.Origin.Renditions) != 0 {
		t.Errorf("renditions = %v, want none", resolved.Origin.Renditions)
	}
}

func TestLivenessComesFromTheDocumentThatListsTheSegments(t *testing.T) {
	for _, tc := range []struct {
		document string
		wantLive bool
	}{{"master_ladder.m3u8", false}, {"media_live.m3u8", true}} {
		stream := hlsAt(t, "http://a.example/"+tc.document)
		// A probe that states no runtime, which alone would read as live.
		stream.Probe = &media.ProbeInfo{}
		if got := resolve(t, sourcetest.Testdata(t, "testdata"), stream).Origin.Live; got != tc.wantLive {
			t.Errorf("%s: Live = %v, want %v", tc.document, got, tc.wantLive)
		}
	}
}

func TestResolveChoosesTheDefaultCompanionAudio(t *testing.T) {
	const master = `#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="French",DEFAULT=NO,URI="audio/fr.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",DEFAULT=YES,URI="audio/en.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="aud"
video.m3u8
`
	resolved := resolve(t, &sourcetest.Documents{ByPath: map[string]string{
		"/master.m3u8":   master,
		"/video.m3u8":    mediaPlaylist,
		"/audio/en.m3u8": mediaPlaylist,
	}}, hlsAt(t, "https://origin.example/master.m3u8"))

	if got := sourcetest.RequireInput(t, resolved.Program, media.AudioInputID).URL.Path; got != "/audio/en.m3u8" {
		t.Errorf("audio URL = %q, want the DEFAULT=YES rendition", got)
	}
	track, ok := resolved.Program.Track(media.TrackAudio)
	if !ok || track.Input != media.AudioInputID || track.Optional {
		t.Errorf("audio track = %+v, want it required of the companion input, which exists only to carry it", track)
	}
}

// A master's CODECS describe the chosen rung in the vocabulary a probe answers in.
func TestResolveDescribesTheChosenRungFromWhatTheMasterDeclared(t *testing.T) {
	for _, tt := range []struct {
		codecs string
		want   *media.ProbeInfo
	}{
		{"avc1.640028,mp4a.40.2", &media.ProbeInfo{VideoCodec: media.CodecH264, VideoProfile: "High", VideoHeight: 1080, VideoBitDepth: 8, VideoLevel: 40, AudioCodec: media.CodecAAC}},
		// Main 10 does not fix the bit depth, so it declares too little to act on.
		{"hvc1.2.4.L120.90,mp4a.40.2", nil},
		{"mp4a.40.2", nil},
	} {
		t.Run(tt.codecs, func(t *testing.T) {
			stream := hlsAt(t, "https://origin.example/master.m3u8")
			stream.Probe = &media.ProbeInfo{VideoCodec: media.CodecHEVC, VideoProfile: "Main 10", VideoHeight: 2160, VideoBitDepth: 10}
			master := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080,CODECS=\"" + tt.codecs + "\"\nvideo.m3u8\n"
			resolved := resolve(t, &sourcetest.Documents{ByPath: map[string]string{
				"/master.m3u8": master,
				"/video.m3u8":  mediaPlaylist,
			}}, stream)
			got, measured := resolved.Program.Measurement()
			if tt.want == nil {
				if measured {
					t.Errorf("measurement = %+v, want none for %q", got, tt.codecs)
				}
				return
			}
			if !measured || !reflect.DeepEqual(got, *tt.want) {
				t.Errorf("measurement = %+v, want the rung's declaration %+v", got, *tt.want)
			}
		})
	}
}

// A key only a DRM licence server applies refuses the link; a clear AES-128 key is castor's to decrypt.
func TestAPlaylistUnderDRMIsRefusedAndAClearKeyIsNot(t *testing.T) {
	for _, tt := range []struct {
		key     string
		refused bool
	}{
		{`#EXT-X-KEY:METHOD=SAMPLE-AES,URI="skd://key",KEYFORMAT="com.apple.streamingkeydelivery",KEYFORMATVERSIONS="1"`, true},
		{`#EXT-X-KEY:METHOD=AES-128,URI="key.bin"`, false},
	} {
		body := "#EXTM3U\n#EXT-X-TARGETDURATION:4\n" + tt.key + "\n#EXTINF:4.0,\nseg0.ts\n#EXT-X-ENDLIST\n"
		_, err := newTestResolver(&sourcetest.Document{Body: body, Status: http.StatusOK}).Resolve(t.Context(), hlsAt(t, "http://a.example/media.m3u8"), source.Rendition{})
		if refused := err != nil; refused != tt.refused {
			t.Errorf("%s: Resolve = %v, want refused %v", tt.key, err, tt.refused)
		}
	}
}
