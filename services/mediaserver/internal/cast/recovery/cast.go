// Package recovery runs a cast's attempts to a verdict: what each failure was, and which cheaper attempt answers it.
package recovery

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// Runner runs one attempt and returns what it did.
type Runner interface {
	Run(ctx context.Context, a Attempt) Outcome
}

// SourceResolver resolves a link, narrowed to a rung once one was chosen.
type SourceResolver interface {
	Resolve(ctx context.Context, s *source.Stream, chosen source.Rendition) (source.Resolution, error)
}

// Cast runs attempts until one works, revising while a fault has an answer and refusing once none does.
func Cast(ctx context.Context, in Intent, run Runner, resolver SourceResolver) error {
	if err := validate(in); err != nil {
		return err
	}
	a, ok := nextReadable(ctx, in, resolver, Attempt{Try: 1, candidate: -1, Delivery: in.Delivery})
	if !ok {
		return fmt.Errorf("none of the %d links could be resolved", len(in.Candidates))
	}
	announce(ctx, in, a)

	led := &ledger{}
	led.admit(a)

	// tried names every strategy already run, for the refusal to state.
	var tried []string

	for {
		slog.InfoContext(ctx, "attempt", "try", a.Try, "candidates", len(in.Candidates), "shape", a.String())
		in.Turns.Attempting(a.Try)

		out := run.Run(ctx, a)
		// Whose account to report is settled before cancellation is, which reads it.
		out.Err = attribute(out.Err, out.Evidence.ReadErr)
		if out.Err == nil {
			return nil
		}

		f := classify(in, a, out, tried)
		if f.kind == cancelled {
			// The cause says why the cast was cancelled; the attempt's error is only the broken pipe that followed.
			return cmp.Or(context.Cause(ctx), out.Err)
		}

		rev := revise(ctx, in, out, f, led, resolver)
		if !rev.offered {
			refused(ctx, in, a, out, f, tried)
			return f
		}

		revising(ctx, f, rev)
		in.Turns.Revising(rev.strategy.action, f.why)
		tried = append(tried, string(rev.strategy.action))
		a = rev.attempt
	}
}

func validate(in Intent) error {
	if len(in.Candidates) == 0 {
		// A caller's bug, not the source's, so it is an error rather than a refusal.
		return errors.New("a cast needs at least one candidate link to attempt")
	}
	for i, candidate := range in.Candidates {
		if candidate == nil || candidate.URL == nil {
			return fmt.Errorf("cast candidate %d has no URL", i)
		}
	}
	return nil
}

func announce(ctx context.Context, in Intent, a Attempt) {
	slog.InfoContext(ctx, "source program",
		"candidates", len(in.Candidates),
		"candidate", a.candidate+1,
		"renditions", len(a.Origin.Renditions),
		"sole", a.Origin.Sole(),
		"segmented", a.Origin.Segmented,
		"segment_framing", a.Origin.Framing,
		"live", a.Origin.Live,
		"duration", a.Origin.Duration,
	)
	primary := a.Fetch.Primary(a.Program)
	slog.InfoContext(ctx, "source read policy",
		"policy", primary.Name,
		"why", primary.Why,
		"inputs", a.Fetch.String(),
		// Zero deadline is valid for some sources, not missing.
		"read_deadline", primary.Deadline,
		"segment_retries", primary.SegmentRetries,
		"readrate", primary.Pace.Realtime,
		"burst", primary.Pace.Burst,
	)
}

func refused(ctx context.Context, in Intent, a Attempt, out Outcome, f *fault, tried []string) {
	slog.WarnContext(ctx, "cast refused",
		"verdict", f.kind.String(),
		"rule", f.rule,
		"why", f.why,
		"reached", out.Evidence.Reached.String(),
		"health", f.evidence.Vitals.String(),
		"candidate", a.candidate+1,
		"candidates", len(in.Candidates),
		"arithmetic", strings.Join(f.arithmetic(), "; "),
		"tried", tried,
	)
}

func revising(ctx context.Context, f *fault, rev revision) {
	slog.WarnContext(ctx, "revising the cast",
		"verdict", f.kind.String(),
		"why", f.why,
		"strategy", rev.strategy.action,
		"expecting", rev.strategy.why,
		"health", f.evidence.Vitals.String(),
		"next", rev.attempt.String(),
	)
}

// ledger refuses an attempt already run, so a strategy table edited wrong cannot loop.
type ledger struct{ ran map[string]bool }

// admit records a and reports whether it had not run yet.
func (l *ledger) admit(a Attempt) bool {
	key := a.key()
	if l.ran[key] {
		return false
	}
	if l.ran == nil {
		l.ran = make(map[string]bool)
	}
	l.ran[key] = true
	return true
}
