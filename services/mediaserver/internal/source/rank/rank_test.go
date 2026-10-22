package rank

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/sourcetest"
)

var testConfig = Config{ProbeMaxConcurrency: 2}

// scripted measures every link its script names as opened, records what it was asked, and fails the rest.
type scripted struct {
	answers map[string]*media.ProbeInfo

	mu    sync.Mutex
	asked []string
}

func (s *scripted) Probe(_ context.Context, c *source.Stream) (source.Inspection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, c.URL.String())
	info, ok := s.answers[c.URL.String()]
	if !ok {
		return source.Inspection{}, fmt.Errorf("no scripted measurement for %s", c.URL)
	}
	return source.Inspection{Info: *info, Reach: media.ReachOpened, ContentType: info.ContentType}, nil
}

// playable is a measurement of a real title: both tracks, a feature runtime.
func playable(bitRate int64, height int, runtime time.Duration) *media.ProbeInfo {
	return &media.ProbeInfo{
		BitRate: bitRate, Duration: runtime, ContentType: media.HLS,
		VideoCodec: media.CodecH264, AudioCodec: media.CodecAAC, VideoHeight: height,
	}
}

func candidateAt(t *testing.T, contentType, raw string) *source.Stream {
	t.Helper()
	return &source.Stream{URL: sourcetest.URL(t, raw), ContentType: contentType}
}

// The per-host cap spends its measurements on the document that promises a ladder, whatever its capture order.
func TestRankMeasuresTheLadderBeforeTheCapDropsIt(t *testing.T) {
	answers := map[string]*media.ProbeInfo{}
	var captured []*source.Stream
	for i := range maxProbePerHost + 1 {
		raw := fmt.Sprintf("http://a.example/v%d.m3u8", i)
		answers[raw] = playable(1_000_000, 720, 2*time.Hour)
		c := candidateAt(t, media.HLS, raw)
		c.Ladder = source.LadderSole
		captured = append(captured, c)
	}
	const master = "http://a.example/index.m3u8"
	answers[master] = playable(0, 1080, 2*time.Hour)
	ladder := candidateAt(t, media.HLS, master)
	ladder.Ladder = source.LadderMultivariant
	captured = append(captured, ladder)

	measurer := &scripted{answers: answers}
	order, err := New(testConfig, 1080, measurer.Probe).Rank(t.Context(), captured)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if asked := measurer.asked; len(asked) != maxProbePerHost || !slices.Contains(asked, master) {
		t.Fatalf("measured %v, want %d links on one host including the master", asked, maxProbePerHost)
	}
	if order[0].URL.String() != master || order[0].Ladder != source.LadderMultivariant {
		t.Errorf("best = %s (ladder %v), want the master carrying its ladder", order[0].URL, order[0].Ladder)
	}
}

func TestCancellationDoesNotAdmitAProbeFailureAsAFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := New(testConfig, 1080, func(ctx context.Context, _ *source.Stream) (source.Inspection, error) {
		return source.Inspection{}, ctx.Err()
	})
	s := candidateAt(t, media.HLS, "https://origin.example/movie.m3u8")
	if _, err := r.Rank(ctx, []*source.Stream{s}); !errors.Is(err, context.Canceled) {
		t.Errorf("Rank = %v, want cancellation", err)
	}
	if _, err := r.Measure(ctx, s); !errors.Is(err, context.Canceled) {
		t.Errorf("Measure = %v, want cancellation", err)
	}
}

func TestFailedProbeKeepsTheInspectedFormat(t *testing.T) {
	r := New(testConfig, 1080, func(context.Context, *source.Stream) (source.Inspection, error) {
		return source.Inspection{ContentType: media.HLS}, errors.New("probe timed out")
	})
	got, err := r.Measure(t.Context(), candidateAt(t, "", "https://origin.example/live"))
	if err != nil || got.ContentType != media.HLS {
		t.Fatalf("Measure = %q, %v; want the HLS reader for a last resort", got.ContentType, err)
	}
}

func TestRankingKeepsTheIdentityInspectionBoundItsObservationUnder(t *testing.T) {
	// The local reading of a DASH origin is HLS; the candidate is still read as DASH.
	r := New(testConfig, 1080, func(context.Context, *source.Stream) (source.Inspection, error) {
		return source.Inspection{Info: *playable(1_000_000, 720, time.Hour), Reach: media.ReachOpened, ContentType: media.DASH}, nil
	})
	got, err := r.Measure(t.Context(), candidateAt(t, "", "https://origin.example/manifest"))
	if err != nil || got.ContentType != media.DASH {
		t.Fatalf("Measure = %q, %v; want the identity inspection bound", got.ContentType, err)
	}
}

func TestRankingCarriesSuccessfulBindingsAndClearsFailedInspection(t *testing.T) {
	s := candidateAt(t, media.HLS, "https://origin.example/master.m3u8")
	program, err := source.ProgramFor(s)
	if err != nil {
		t.Fatal(err)
	}
	program.Inputs[0].URL.Path = "/selected.m3u8"
	info := *playable(1_000_000, 720, time.Hour)
	observed := media.Observe(program, info)
	var fail bool
	r := New(testConfig, 1080, func(context.Context, *source.Stream) (source.Inspection, error) {
		inspection := source.Inspection{Info: info, Reach: media.ReachOpened, Observation: &observed, ContentType: media.HLS}
		if fail {
			return inspection, errors.New("inspection failed after partial output")
		}
		return inspection, nil
	})
	measured, err := r.Measure(t.Context(), s)
	if err != nil || measured.Observation == nil {
		t.Fatalf("ranked candidate lost bindings: %+v, %v", measured, err)
	}
	if got, ok := measured.Observation.For(program); !ok || got.VideoHeight != 720 {
		t.Fatalf("ranked observation = %+v, bound=%t", got, ok)
	}
	fail = true
	failed, err := r.Measure(t.Context(), measured)
	if err != nil || failed.Observation != nil || failed.Probe != nil {
		t.Fatalf("failed reinspection retained evidence: %+v, %v", failed, err)
	}
}
