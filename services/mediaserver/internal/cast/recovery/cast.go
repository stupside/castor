// Package recovery runs a cast's attempts to a verdict: what each failure was, and which cheaper attempt answers it.
package recovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/stupside/castor/internal/telemetry"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// Runner runs one attempt and returns only after joining its work and releasing
// its resources. Recovery may start the next attempt as soon as Run returns.
type Runner interface {
	Run(ctx context.Context, a Attempt) Outcome
}

// SourceResolver resolves a link, narrowed to a rung once one was chosen and to rungs a rate ceiling leaves.
type SourceResolver interface {
	Resolve(ctx context.Context, s *source.Stream, chosen source.Rendition, ceiling media.Bitrate) (source.Resolution, error)
}

// Cast runs attempts until one works, revising while a fault has an answer and refusing once none does.
func Cast(ctx context.Context, in Intent, run Runner, resolver SourceResolver) error {
	if err := validate(in); err != nil {
		return err
	}
	a, ok := nextReadable(ctx, in, resolver, Attempt{Try: 1, candidate: -1, Delivery: in.Delivery})
	if !ok {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return fmt.Errorf("none of the %d links could be resolved", len(in.Candidates))
	}
	if a, ok = a.at(in.Start); !ok {
		return fmt.Errorf("the source has no position %s to begin at", in.Start)
	}

	led := &ledger{}
	led.admit(a)

	// tried names every strategy already run, for the refusal to state.
	var tried []string

	for {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		slog.InfoContext(ctx, "attempt", "try", a.Try, "candidate", a.candidate+1, "candidates", len(in.Candidates))
		in.Turns.Attempting(a.Try)

		out := run.Run(ctx, a)
		// Whose account to report is settled before cancellation is, which reads it.
		out.Err = attribute(out.Err, out.Evidence.ReadErr)
		if out.Err == nil {
			return nil
		}

		f := classify(a, out, tried)
		if f.kind == cancelled {
			// Cancellation explains why work stopped; it must not erase a
			// separate failure returned after the attempt joined its resources.
			cause := context.Cause(ctx)
			if cause == nil || errors.Is(out.Err, cause) {
				return out.Err
			}
			return errors.Join(cause, out.Err)
		}

		revisionCtx, finish := telemetry.Start(ctx, telemetry.Recovery)
		rev := revise(revisionCtx, in, out, f, led, resolver)
		if !rev.offered {
			finish(f)
			return f
		}
		finish(nil)

		revising(ctx, f, rev)
		in.Turns.Revising(rev.action, f.why)
		tried = append(tried, string(rev.action))
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

func revising(ctx context.Context, f *fault, rev revision) {
	telemetry.RecoveryRevised(ctx, string(rev.action))
	slog.WarnContext(ctx, "revising the cast",
		"verdict", f.kind.String(),
		"error", f.err,
		"strategy", rev.action,
	)
}

// ledger refuses an attempt already run, so a strategy table edited wrong cannot loop.
type ledger struct{ ran map[identity]bool }

// admit records a and reports whether it had not run yet.
func (l *ledger) admit(a Attempt) bool {
	id := a.identify()
	if l.ran[id] {
		return false
	}
	if l.ran == nil {
		l.ran = make(map[identity]bool)
	}
	l.ran[id] = true
	return true
}
