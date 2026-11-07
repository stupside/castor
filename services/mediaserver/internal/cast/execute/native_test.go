package execute

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/compose"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
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

type liveFallbackOrigin struct{ program media.Program }

func (o liveFallbackOrigin) Resolve(context.Context, *source.Stream, source.Rendition, media.Bitrate) (source.Resolution, error) {
	return source.Resolution{Program: o.program.Clone(), Origin: source.Origin{Live: true}}, nil
}

type liveFallbackTurns struct{ actions []recovery.Action }

func (*liveFallbackTurns) Attempting(int) {}
func (t *liveFallbackTurns) Revising(action recovery.Action, _ string) {
	t.actions = append(t.actions, action)
}

func TestNativeLiveFallbackProducesPlayableMediaThroughBothReadPaths(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	file := generateFixture(t, ffmpegPath, "live.ts", 8,
		[]string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-g", "50", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", media.MPEGTS)
		http.ServeFile(w, r, file)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL + "/live.ts")
	stream := &source.Stream{URL: u, ContentType: media.MPEGTS}
	program := programFromStream(t, stream)
	// A finite fixture bounds the test while exercising the live source policy.
	program.Inputs[0].Fetch.Live = true
	for _, selfFetch := range []bool{true, false} {
		t.Run(map[bool]string{true: "remux", false: "read-once"}[selfFetch], func(t *testing.T) {
			dev := &fakeDevice{caps: chromecastLike(media.MP4), drain: true}
			dev.caps.SelfFetch = selfFetch
			cast := realCast(dev, ffmpegPath, ffprobePath)
			cast.Encoders = func(_ context.Context, codec media.Codec) (ffmpeg.Encoder, bool) {
				return ffmpeg.Encoder{Name: "libx264", Codec: media.CodecH264}, codec == media.CodecH264
			}
			cast.PreserveSource = true
			turns := &liveFallbackTurns{}
			ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
			defer cancel()
			err := recovery.Cast(ctx, recovery.Intent{
				Candidates: []*source.Stream{stream}, PreserveSource: true,
				Delivery: compose.DeliveryServe, Deadline: testReadDeadline, Turns: turns,
			}, cast, liveFallbackOrigin{program})
			if err != nil {
				t.Fatalf("live fallback: %v", err)
			}
			if len(dev.served) == 0 {
				t.Fatal("the receiver got no media")
			}
			if len(turns.actions) != 1 || turns.actions[0] != recovery.OptimizeLive {
				t.Fatalf("revisions = %v; want one optimized live retry", turns.actions)
			}
			if !cast.PreserveSource {
				t.Fatal("the fallback overwrote the casting preference")
			}
		})
	}
}

func TestLiveOptimizationOnlyChangesTheAttemptPreparation(t *testing.T) {
	cast := Cast{PreserveSource: true}
	for _, optimized := range []bool{false, true} {
		p := testPreparation(t.Context(), cast, recovery.Attempt{OptimizedLive: optimized})
		if p.PreserveSource == optimized {
			t.Fatalf("optimized=%v but preserve_source=%v", optimized, p.PreserveSource)
		}
	}
	if !cast.PreserveSource {
		t.Fatal("the cast's saved request changed")
	}
}
