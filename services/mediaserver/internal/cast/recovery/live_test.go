package recovery

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

type liveTurns struct{ actions []Action }

func (*liveTurns) Attempting(int)                     {}
func (t *liveTurns) Revising(action Action, _ string) { t.actions = append(t.actions, action) }

func TestNativeLiveRetriesWithOptimizationOnlyWhenNeeded(t *testing.T) {
	refused := Outcome{Err: fmt.Errorf("planning media: %w: [spliced spliced]", media.ErrNativeIncompatible)}
	for _, tc := range []struct {
		name         string
		live, native bool
		outcomes     []Outcome
		attempts     int
		wantError    bool
	}{
		{"live needs conversion", true, true, []Outcome{refused, delivered}, 2, false},
		{"live already plays natively", true, true, []Outcome{delivered}, 1, false},
		{"finite native stays strict", false, true, []Outcome{refused, delivered}, 1, true},
		{"optimized is not retried as optimized", true, false, []Outcome{refused, delivered}, 1, true},
		{"conversion can still fail", true, true, []Outcome{refused, refused, delivered}, 2, true},
		{"unrelated failure", true, true, []Outcome{{Err: errors.New("network failure")}, delivered}, 1, true},
		{"cancelled", true, true, []Outcome{{Err: refused.Err, Evidence: Evidence{Cancelled: true}}, delivered}, 1, true},
		{"after playback", true, true, []Outcome{{Err: refused.Err, Evidence: Evidence{Reached: health.Playing}}, delivered}, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			turns := &liveTurns{}
			in := Intent{PreserveSource: tc.native, Turns: turns, Candidates: candidates(t, "https://cdn.example/stream.m3u8"), Deadline: 30 * time.Second}
			run := &scriptedRunner{outcomes: tc.outcomes}
			err := Cast(t.Context(), in, run, publishing(in.Candidates[0], source.Origin{Segmented: true, Live: tc.live}, source.Rendition{}))
			if (err != nil) != tc.wantError || len(run.seen) != tc.attempts {
				t.Fatalf("cast = %v after %d attempts; want error=%v, attempts=%d", err, len(run.seen), tc.wantError, tc.attempts)
			}
			optimized := slices.Contains(turns.actions, Action("optimize-live"))
			if optimized != (tc.attempts == 2) {
				t.Fatalf("revisions = %v; optimization must be reported only for the fallback", turns.actions)
			}
			if tc.attempts == 2 && (!run.seen[0].Program.SameBindings(run.seen[1].Program) || run.seen[1].Program.Start != 0) {
				t.Fatal("optimization changed the chosen source instead of restarting at its live edge")
			}
			if in.PreserveSource != tc.native {
				t.Fatal("the fallback changed the caller's preference")
			}
		})
	}
}
