package recovery

import (
	"fmt"
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
)

// kind classifies attempt failures: two failures share a kind exactly when the same change would answer both.
type kind int

const (
	// unclassified is a real verdict before any row has spoken, not an error.
	unclassified kind = iota
	// cancelled is the cast's context ending, not a failure.
	cancelled
	resourceExhausted
	// unreachable is a link that established no media and never stated a speed.
	unreachable
	// sourceStalled is a source that stopped delivering mid-cast.
	sourceStalled
	// starved is a device that took everything the read had made while the read was still running.
	starved
	// tooSlow is a source measured under playback pace before any device was pointed at it.
	tooSlow
	// copyBrokeUpstream is a reader that exited on packets it was copying.
	copyBrokeUpstream
	// deviceGone is a device that crashed or switched off, not one that refused.
	deviceGone
	// deviceRefused is a device that would not play the URL or never came for bytes.
	deviceRefused
	// producedNothing is a delivery that ended without an artifact to fetch.
	producedNothing
	// outranked is a cast that played well on a link another outranks.
	outranked
)

func (k kind) String() string {
	switch k {
	case resourceExhausted:
		return "resource-exhausted"
	case cancelled:
		return "cancelled"
	case unreachable:
		return "unreachable"
	case sourceStalled:
		return "source-stalled"
	case starved:
		return "starved"
	case tooSlow:
		return "too-slow"
	case copyBrokeUpstream:
		return "copy-broke-upstream"
	case deviceGone:
		return "device-gone"
	case deviceRefused:
		return "device-refused"
	case producedNothing:
		return "produced-nothing"
	case outranked:
		return "outranked"
	default:
		return "unclassified"
	}
}

type classRule struct {
	why  string
	when func(Evidence) bool
	kind kind
}

// classes is the ordered classification rules for failed attempts; every discriminator is STRUCTURAL.
var classes = []classRule{{
	// First, because cancellation is upstream of every symptom below; decided from context, not error text.
	why:  "cancelled",
	when: func(e Evidence) bool { return e.Cancelled },
	kind: cancelled,
}, {
	why:  "the local resource budget is exhausted",
	when: func(e Evidence) bool { return e.ResourceErr != nil },
	kind: resourceExhausted,
}, {
	why:  "the device stopped answering",
	when: func(e Evidence) bool { return e.DeviceGone != nil },
	kind: deviceGone,
}, {
	// Only castor reads such a timeline, so no other policy on this link reads it either.
	why:  "the link's timeline is unreadable",
	when: func(e Evidence) bool { return e.TimelineErr != nil },
	kind: unreachable,
}, {
	// Media landed then died is deliberately not unreachable; that would send a working cast to recovery.
	why: "the link delivered nothing",
	when: func(e Evidence) bool {
		return (e.Verdict == health.Dead || e.Verdict == health.Undeliverable) && e.Reached <= health.Reading && e.Vitals.Landed == 0 && e.Vitals.Samples == 0
	},
	kind: unreachable,
}, {
	// A cold or failing edge truncates or drops segments; a second read of the same link may get them whole.
	why:  "the source ended short of its declared length",
	when: func(e Evidence) bool { return e.Reached <= health.Reading && e.ReadIncomplete },
	kind: sourceStalled,
}, {
	// Keyed on READER exit status and copied axes; castor kills the reader on its own faults.
	why: "the source cannot be copied as it is",
	when: func(e Evidence) bool {
		return e.Reached <= health.Reading && e.ReadExit > 0 && e.Copied.Any()
	},
	kind: copyBrokeUpstream,
}, {
	// One change (stop asking device to fetch) answers all three shapes at different moments.
	why: "the device refused the stream",
	when: func(e Evidence) bool {
		return e.PlayErr != nil || e.Undelivered != nil || e.Verdict == health.Unfetched
	},
	kind: deviceRefused,
}, {
	// Container refuses tracks at header-write time or reader's every segment answered 404; or it fell silent over a buffer already proven.
	why: "the delivery produced nothing",
	when: func(e Evidence) bool {
		return e.Reached == health.Opening && (e.Verdict == health.Dead || e.Verdict == health.Stalled && e.Buffered)
	},
	kind: producedNothing,
}}

var verdictClasses = map[health.Kind]classRule{
	// It already waited out two reconnect ceilings.
	health.Stalled: {
		why:  "the source stopped delivering",
		kind: sourceStalled,
	},
	health.Starved: {
		why:  "the source delivers less than playback consumes",
		kind: starved,
	},
	health.Outranked: {
		why:  "a better link is worth trying",
		kind: outranked,
	},
	health.Undeliverable: {
		why:  "the source is too slow to play",
		kind: tooSlow,
	},
}

// unrecognised is the fallback row; fault still carries phase, measurements and evidence.
var unrecognised = classRule{
	why:  "the attempt failed",
	kind: unclassified,
}

// classify names what failed, after the strategies already tried.
func classify(a Attempt, o Outcome, tried []string) *fault {
	r := classFor(o.Evidence)
	return &fault{
		kind:     r.kind,
		why:      r.why,
		attempt:  a,
		evidence: o.Evidence,
		tried:    tried,
		err:      o.Err,
	}
}

func classFor(e Evidence) classRule {
	for _, r := range classes {
		if r.when(e) {
			return r
		}
	}
	if r, ok := verdictClasses[e.Verdict]; ok {
		return r
	}
	return unrecognised
}

type fault struct {
	kind kind
	why  string
	// attempt is the one that failed, which a strategy revises.
	attempt  Attempt
	evidence Evidence
	// tried distinguishes 'could not cast this' from 'tried three things and here is what each measured'.
	tried []string
	// err is the attempt's own error, unwrapped so errors.Is still finds cancellation or exit status.
	err error
}

func (f *fault) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", f.kind, f.why)
	if len(f.tried) > 0 {
		fmt.Fprintf(&b, "; already tried: %s", strings.Join(f.tried, ", "))
	}
	if gone := f.evidence.DeviceGone; gone != nil {
		fmt.Fprintf(&b, "; the device %q stopped answering", gone.Device)
	}
	if lines := tells(f.evidence.Lines); len(lines) > 0 {
		fmt.Fprintf(&b, "; the reader said: %s", strings.Join(lines, " | "))
	}
	if f.err != nil {
		fmt.Fprintf(&b, ": %s", f.err)
	}
	return b.String()
}

// tells quotes the last few complaints the blamed party printed; it never decides a class.
func tells(lines []string) []string {
	return lines[max(0, len(lines)-4):]
}

func (f *fault) Unwrap() error { return f.err }
