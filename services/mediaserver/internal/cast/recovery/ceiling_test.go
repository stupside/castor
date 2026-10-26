package recovery

import (
	"context"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// ceilingsSeen is a resolver that records the ceiling every resolution asked for.
type ceilingsSeen struct {
	fakeProgram
	asked []media.Bitrate
}

func (r *ceilingsSeen) Resolve(ctx context.Context, s *source.Stream, chosen source.Rendition, ceiling media.Bitrate) (source.Resolution, error) {
	r.asked = append(r.asked, ceiling)
	return r.fakeProgram.Resolve(ctx, s, chosen, ceiling)
}

func TestEveryLinkOfACastIsReadUnderTheCeilingItStartedWith(t *testing.T) {
	in := Intent{Turns: unheard{}, Candidates: candidates(t, "https://cdn.example/one.m3u8", "https://other.example/two.m3u8"), Deadline: 30 * time.Second, Ceiling: 4_000_000}
	resolver := &ceilingsSeen{}
	run := &scriptedRunner{outcomes: []Outcome{judged(health.Dead, health.Reading, health.Vitals{}), delivered}}

	_ = Cast(t.Context(), in, run, resolver)
	if len(resolver.asked) < 2 {
		t.Fatalf("resolved %d links, want the first and the one recovery moved to", len(resolver.asked))
	}
	for i, ceiling := range resolver.asked {
		if ceiling != 4_000_000 {
			t.Errorf("link %d was read under %s, want the cast's 4.0 Mbit/s", i+1, ceiling)
		}
	}
}
