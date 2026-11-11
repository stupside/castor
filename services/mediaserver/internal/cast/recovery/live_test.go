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

// Once optimize-live has allowed the conversion, an encode that hardware failed is made again in software on the same source.
func TestAnOptimizedLiveEncodeThatFailedInHardwareIsMadeAgainInSoftware(t *testing.T) {
	refused := Outcome{Err: fmt.Errorf("planning media: %w", media.ErrNativeIncompatible)}
	for _, tc := range []struct {
		name   string
		played time.Duration
	}{
		{"before playback", 0},
		{"while playing", 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			turns := &liveTurns{}
			in := Intent{PreserveSource: true, Turns: turns, Candidates: candidates(t,
				"https://cdn.example/live.m3u8", "https://cdn.example/other.m3u8"), Deadline: 30 * time.Second}
			run := &scriptedRunner{outcomes: []Outcome{refused, encodeFailedAt(tc.played, true), delivered}}
			err := Cast(t.Context(), in, run, publishing(in.Candidates[0], source.Origin{Segmented: true, Live: true}, source.Rendition{}))
			if err != nil || len(run.seen) != 3 {
				t.Fatalf("cast = %v after %d attempts, revisions %v; want the native refusal, the optimized encode and its software retry", err, len(run.seen), turns.actions)
			}
			if want := []Action{OptimizeLive, SoftwareEncode}; !slices.Equal(turns.actions, want) {
				t.Errorf("revisions = %v, want %v", turns.actions, want)
			}
			last := run.seen[2]
			if !last.Software || !last.OptimizedLive || !run.seen[1].Program.SameBindings(last.Program) {
				t.Errorf("the software retry is %+v, want the same source, still optimized, in software", last)
			}
		})
	}
}

// A cast that preserves its source keeps to it: a failed encode is not made again in software while the cast is still native, and never moves to another link.
func TestAPreservedSourceIsNeitherMadeInSoftwareWhileNativeNorLeftForAnotherLink(t *testing.T) {
	refused := Outcome{Err: fmt.Errorf("planning media: %w", media.ErrNativeIncompatible)}
	for _, tc := range []struct {
		name     string
		live     bool
		outcomes []Outcome
		attempts int
	}{
		{"a finite native cast", false, []Outcome{encodeFailedAt(0, true), delivered}, 1},
		{"a live cast still native", true, []Outcome{encodeFailedAt(0, true), delivered}, 1},
		{"a live cast still native, while playing", true, []Outcome{encodeFailedAt(30*time.Second, true), delivered}, 1},
		{"an optimized live cast whose software encode failed too", true, []Outcome{refused, encodeFailedAt(0, true), encodeFailedAt(0, false), delivered}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := Intent{PreserveSource: true, Turns: &liveTurns{}, Candidates: candidates(t,
				"https://cdn.example/original.m3u8", "https://cdn.example/other.m3u8"), Deadline: 30 * time.Second}
			run := &scriptedRunner{outcomes: tc.outcomes}
			err := Cast(t.Context(), in, run, publishing(in.Candidates[0], source.Origin{Segmented: true, Live: tc.live}, source.Rendition{}))
			if err == nil || len(run.seen) != tc.attempts {
				t.Fatalf("cast = %v after %d attempts; want it to fail after %d", err, len(run.seen), tc.attempts)
			}
			for i, seen := range run.seen {
				if !run.seen[0].Program.SameBindings(seen.Program) {
					t.Errorf("attempt %d left the source the cast preserves", i+1)
				}
			}
		})
	}
}
