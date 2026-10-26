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
	// phases is where the row applies; the action table decides what to do.
	phases []Phase
	when   func(Vitals) bool
	kind   Kind
	blames party
}

// rules are asked in order, the first match deciding; the two rows below answer the rest.
var rules = []rule{{
	// A partial buffer would play, then stop, so a read failing before play is dead.
	name:   "producer-failed",
	phases: []Phase{Reading, Opening},
	when:   func(h Vitals) bool { return h.failed },
	kind:   Dead,
}, {
	// Ahead of the ready rows: a read that never landed its cushion at a pace pauses can carry has nothing to start a device on.
	name:   "undeliverable",
	phases: []Phase{Reading},
	when:   func(h Vitals) bool { return !h.ended && !h.worthStarting() && h.sinceDeficit > deliverBudget },
	kind:   Undeliverable,
}, {
	// Such as a container refusing the codec at its header.
	name:   "produced-nothing",
	phases: []Phase{Opening},
	when:   func(h Vitals) bool { return h.ended && !h.playable() },
	kind:   Dead,
}, {
	// A delivery with no patience of its own would otherwise wait on a silent upstream forever.
	name:   "produced-nothing-yet",
	phases: []Phase{Opening},
	when:   func(h Vitals) bool { return !h.ended && !h.playable() && h.sinceGrowth > StallWindow },
	kind:   Stalled,
}, {
	// A device the watch cannot hold is left to play out what it has; silence past that is a stall.
	name:   "stalled",
	phases: []Phase{Playing},
	when:   func(h Vitals) bool { return !h.holdable && !h.ended && h.sinceGrowth > StallWindow && h.ahead() <= 0 },
	kind:   Stalled,
}, {
	name:   "unfetched",
	phases: []Phase{Playing},
	when:   func(h Vitals) bool { return h.handed == 0 && h.sinceFetch > fetchWindow },
	kind:   Unfetched,
	blames: theDevice,
}, {
	// Before the hold: holding a device whose link cannot carry the cast only moves the stall.
	name:   "choked",
	phases: []Phase{Playing},
	when:   func(h Vitals) bool { return !h.paused && !h.viewerHeld && h.choking() },
	kind:   Choked,
	blames: theDevice,
}, {
	name:   "starved",
	phases: []Phase{Playing},
	when:   func(h Vitals) bool { return h.paused && (h.sincePause > pauseBudget || h.crawling()) },
	kind:   Starved,
}, {
	// The read ending leaves nothing to wait for; the device plays out what it has.
	name:   "replenished",
	phases: []Phase{Playing},
	when:   func(h Vitals) bool { return h.paused && (h.ended || h.ahead() >= highWater) },
	kind:   Replenished,
}, {
	name:   "lean",
	phases: []Phase{Playing},
	when: func(h Vitals) bool {
		return h.holdable && !h.paused && !h.viewerHeld && !h.ended && h.handed > 0 && h.ahead() < lowWater
	},
	kind: Lean,
}, {
	// Before the cast lifts itself: a better link is worth more than a heavier rate of this one.
	name:   "outranked",
	phases: []Phase{Playing},
	when:   func(h Vitals) bool { return h.outranked() },
	kind:   Outranked,
	blames: theDevice,
}, {
	name:   "roomy",
	phases: []Phase{Playing},
	when:   func(h Vitals) bool { return h.roomy() },
	kind:   Roomy,
}, {
	name:   "burn-in-ready",
	phases: []Phase{Reading},
	when: func(h Vitals) bool {
		return h.subtitles && (h.playable() && h.worthStarting() && h.leads() || h.ended)
	},
	kind: ready,
}, {
	name:   "ready",
	phases: []Phase{Reading},
	when: func(h Vitals) bool {
		return !h.subtitles && (h.playable() && h.worthStarting() || h.ended)
	},
	kind: ready,
}, {
	name:   "artifact-ready",
	phases: []Phase{Opening},
	when:   func(h Vitals) bool { return h.playable() || h.overdue },
	kind:   ready,
}}

// nothingEstablished answers both phases before playback when no rule did: nothing has been established.
var nothingEstablished = rule{name: "starting", kind: starting}

// nothingAgainst answers the playing phase when no rule did: a cast in flight with nothing against it.
var nothingAgainst = rule{name: "healthy", kind: healthy}

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
