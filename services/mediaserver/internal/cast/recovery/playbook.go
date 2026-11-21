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

	// Hardware first: it is what real input most often breaks.
	encodeFailed: {softwareEncode, switchCandidate},

	// A cast that played well is moved back toward the link that outranks it, and resumes where it was when none can be read.
	outranked: {climb, stay},
}

// revision is the attempt a strategy offers; offering nothing is ordinary.
type revision struct {
	attempt Attempt
	action  Action
	offered bool
}

// revise is the first strategy in the playbook that offers an attempt not run yet.
func revise(ctx context.Context, in Intent, o Outcome, f *fault, led *ledger, resolver SourceResolver) revision {
	if !revisable(f) {
		return revision{}
	}

	pos, _ := f.resumeAt()
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

// resumeAt is where a cast that played is taken up, and whether that is known: the viewer's place, or a live program's edge.
func (f *fault) resumeAt() (time.Duration, bool) {
	switch {
	case f.attempt.Live():
		return 0, true
	case f.evidence.Reached >= health.Playing:
		at, placed := f.evidence.Vitals.Reached()
		return f.evidence.Began + at, placed
	}
	return f.attempt.Program.Start, true
}

// fallBack returns a climb whose better link failed before playing to the attempt it left.
func fallBack(left *Attempt, f *fault, led *ledger) revision {
	if left == nil || f.evidence.Reached >= health.Playing || f.kind == deviceGone {
		return revision{}
	}
	back := *left
	back.Try = f.attempt.Try + 1
	return revision{attempt: back, action: SwitchCandidate, offered: led.admit(back)}
}

// revisable is an attempt that failed before playing and can change; once playing, one whose kind resumes from a known place.
func revisable(f *fault) bool {
	e := f.evidence
	if e.Reached >= health.Playing {
		switch f.kind {
		case starved, sourceStalled, outranked, encodeFailed:
			_, known := f.resumeAt()
			return known
		}
		return false
	}
	if judged, ok := errors.AsType[*health.Fault](f.err); ok {
		return judged.Revise
	}
	return e.PlayErr != nil || e.TimelineErr != nil || e.EncodeErr != nil
}
