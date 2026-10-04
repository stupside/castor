package health

import (
	"fmt"
	"slices"
)

// party is whom a verdict blames, and so whose evidence explains it.
type party int

const (
	theProducer party = iota
	theDevice
)

// rule is one row of the judgement table; a caller sees the Fault it reaches.
type rule struct {
	// name identifies the row in logs.
	name string
	// why is the row's reasoning, carried to the user.
	why string
	// phases is where the row applies; the action table decides what to do.
	phases []Phase
	// when is nil on the fallback rows, which are never asked.
	when   func(Vitals) bool
	kind   Kind
	blames party
}

// rules are asked in order, the first match deciding; the fallback rows answer the rest.
var rules = []rule{{
	// A partial buffer would play, then stop, so a read failing before play is dead.
	name:   "read-failed",
	why:    "the source read reached a terminal error before playback could start",
	phases: []Phase{Reading},
	when:   func(h Vitals) bool { return h.failed },
	kind:   Dead,
}, {
	// Such as a container refusing the codec at its header.
	name:   "produced-nothing",
	why:    "the producer ended without writing anything a device could fetch",
	phases: []Phase{Opening},
	when:   func(h Vitals) bool { return h.ended && !h.playable() },
	kind:   Dead,
}, {
	// A delivery with no patience of its own would otherwise wait on a silent upstream forever.
	name:   "produced-nothing-yet",
	why:    "the producer is still running but has written nothing a device could fetch for the whole stall window; the likeliest cause is an upstream that accepted the connection and never sent a byte",
	phases: []Phase{Opening},
	when:   func(h Vitals) bool { return !h.ended && !h.playable() && h.sinceGrowth > StallWindow },
	kind:   Stalled,
}, {
	name:   "stalled",
	why:    "the producer stopped delivering and the device has played everything that reached it",
	phases: []Phase{Reading, Playing},
	when:   func(h Vitals) bool { return !h.ended && h.sinceGrowth > StallWindow && !h.buffered() },
	kind:   Stalled,
}, {
	name:   "unfetched",
	why:    "the device accepted the stream URL and was never handed a byte of what this cast produced for it",
	phases: []Phase{Playing},
	when:   func(h Vitals) bool { return h.handed == 0 && h.sinceFetch > fetchWindow },
	kind:   Unfetched,
	blames: theDevice,
}, {
	name:   "burn-in-ready",
	why:    "the buffer holds a cushion of media and the transcription is far enough ahead of the encoder",
	phases: []Phase{Reading},
	when: func(h Vitals) bool {
		return h.subtitles && (h.playable() && h.cushioned() && h.leads() || h.ended)
	},
	kind: ready,
}, {
	name:   "ready",
	why:    "the buffer holds a cushion of media",
	phases: []Phase{Reading},
	when: func(h Vitals) bool {
		return !h.subtitles && (h.playable() && h.cushioned() || h.ended)
	},
	kind: ready,
}, {
	name:   "artifact-ready",
	why:    "the artifact a device fetches exists, or this delivery has waited as long as it is willing to",
	phases: []Phase{Opening},
	when:   func(h Vitals) bool { return h.playable() || h.overdue },
	kind:   ready,
}}

// nothingEstablished answers both phases before playback when no rule did: nothing has been established.
var nothingEstablished = rule{name: "starting", why: "nothing has been established yet", kind: starting}

// nothingAgainst answers the playing phase when no rule did: a cast in flight with nothing against it.
var nothingAgainst = rule{name: "healthy", why: "nothing is against this cast", kind: healthy}

func judge(p Phase, h Vitals) (rule, action, error) {
	r := nothingEstablished
	if p == Playing {
		r = nothingAgainst
	}
	for _, candidate := range rules {
		if slices.Contains(candidate.phases, p) && candidate.when(h) {
			r = candidate
			break
		}
	}
	act, ok := actions[verdict{kind: r.kind, phase: p}]
	if !ok {
		return rule{}, 0, fmt.Errorf("rule %q reached a %s verdict in the %s phase, which has no action", r.name, r.kind, p)
	}
	return r, act, nil
}
