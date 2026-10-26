package recovery

import (
	"context"
	"errors"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
)

// playbook is each fault kind's recoveries, cheapest first; switching candidate last; a kind absent here has none.
var playbook = map[kind][]strategy{
	// Nothing about this link was established, so nothing on it is worth changing.
	unreachable: {switchCandidate},

	// A link that went silent may have rate-limited the opening burst, so it is asked once more at playback pace.
	sourceStalled: {relaxRead, switchCandidate},

	// A slower pace would not make this link faster; the next one may be.
	tooSlow: {switchCandidate},

	// A device that outran the read mid-cast is asked again from where it got to: the same link at playback pace, then the next.
	starved: {relaxRead, switchCandidate},

	// Decoding comes first; a bitstream this one cannot copy may still copy from another link.
	copyBrokeUpstream: {decodeAxis, switchCandidate},

	// A device refusing the URL, or never coming for it, is served instead; the next link, in case the source is the problem.
	deviceRefused: {serveInstead, switchCandidate},

	// The delivery lost the artifact, not the reader, so decoding would answer nothing.
	producedNothing: {switchCandidate},
}

// revision is the attempt a strategy offers; offering nothing is ordinary.
type revision struct {
	attempt Attempt
	action  Action
	offered bool
}

// revise is the first strategy in the playbook that offers an attempt not run yet.
func revise(ctx context.Context, in Intent, o Outcome, f *fault, led *ledger, resolver SourceResolver) revision {
	if !revisable(o) {
		return revision{}
	}

	pos := f.resumeAt()
	facts := change{intent: in, attempt: f.attempt, outcome: o, resolver: resolver}
	for _, s := range playbook[f.kind] {
		next, ok := s.apply(ctx, facts)
		if !ok {
			continue
		}
		if next, ok = next.at(pos); !ok {
			continue
		}
		next.Try = f.attempt.Try + 1
		if !led.admit(next) {
			continue
		}
		return revision{attempt: next, action: s.action, offered: true}
	}
	return revision{}
}

// resumeAt is where the viewer got to: a cast that played is taken up there.
func (f *fault) resumeAt() time.Duration {
	switch {
	case f.attempt.live():
		// A live program has no position to go back to, only its edge.
		return 0
	case f.evidence.Reached >= health.Playing:
		return f.evidence.Began + f.evidence.Vitals.Reached()
	}
	return f.attempt.Program.Start
}

// revisable is an attempt that failed before playing and can change; once playing, only one that can resume where it got to.
func revisable(o Outcome) bool {
	if o.Evidence.Reached >= health.Playing {
		switch o.Evidence.Verdict {
		case health.Starved, health.Stalled:
			return true
		}
		return false
	}
	if judged, ok := errors.AsType[*health.Fault](o.Err); ok {
		return judged.Revise
	}
	return o.Evidence.PlayErr != nil || o.Evidence.TimelineErr != nil
}
