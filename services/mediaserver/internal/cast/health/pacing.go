package health

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"time"

	"github.com/stupside/castor/internal/machine"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

const (
	// linkShare of what the device took is asked of it; the rest rides out the link's swings.
	linkShare = 0.75

	// lowest is the smallest picture and its sound; nothing lighter is worth sending.
	lowest media.Bitrate = 1_000_000

	// step is the grain a rate is rounded down to.
	step media.Bitrate = 50_000

	// liftBy applies when nothing is known of where the link gives out.
	liftBy = 1.25

	// settle lets the device's intake be measured over the new rate alone.
	settle = intakeSpan + 5*time.Second

	// probeFor is how long a heavier rate is on trial; it is given back the moment the device falls short.
	probeFor = settle + 15*time.Second

	// liftAfter is how long a rate must run well before more is asked; climbAfter once a heavier rate has held.
	liftAfter  = 3 * time.Minute
	climbAfter = 90 * time.Second

	// avoid is how long a rate the link did not carry is left alone, doubling per failed trial up to avoidMost.
	avoid     = 10 * time.Minute
	avoidMost = 40 * time.Minute
)

// A heavier rate is on trial until it holds or is given back; one that held shortens the wait for the next.
type rateState uint8

const (
	rateSteady rateState = iota
	rateTrial
	rateProven
)

func (s rateState) String() string {
	return [...]string{
		rateSteady: "steady",
		rateTrial:  "trial",
		rateProven: "proven",
	}[s]
}

// rateEvent is a move of the rate: the verb a change of rate is told by.
type rateEvent uint8

const (
	rateLifted rateEvent = iota
	rateLightened
	rateGivenBack
	rateHeld
)

func (e rateEvent) String() string {
	return [...]string{
		rateLifted:    "lifted",
		rateLightened: "lightened",
		rateGivenBack: "given-back",
		rateHeld:      "held",
	}[e]
}

func rateMachine() *machine.Machine[rateState, rateEvent] {
	return machine.New("rate", rateSteady, nil, []machine.Edge[rateState, rateEvent]{
		{On: rateLifted, From: []rateState{rateSteady, rateProven}, To: rateTrial},
		{On: rateLightened, From: []rateState{rateSteady, rateProven}, To: rateSteady},
		{On: rateGivenBack, From: []rateState{rateTrial}, To: rateSteady},
		{On: rateHeld, From: []rateState{rateTrial}, To: rateProven},
	}, nil)
}

// pacing is the rate a cast asks of a device.
type pacing struct {
	// policy is the ceiling, zero for none; at is the rate asked now, zero for as the source weighs.
	policy media.Bitrate
	at     media.Bitrate
	// full is what the cast weighs asked of nothing, known once it was first lightened.
	full media.Bitrate
	// since is when the rate last changed, zero if it never did.
	since time.Time

	// bad is the lightest rate the link did not carry, avoided until until; backoff is the next avoidance.
	bad     media.Bitrate
	until   time.Time
	backoff time.Duration

	// before is the rate a heavier one on trial is given back for.
	before  media.Bitrate
	machine *machine.Machine[rateState, rateEvent]
}

// change is a rate pacing asks for: the move that reaches it, and what the cast weighed when it was asked.
type change struct {
	move    rateEvent
	to      media.Bitrate
	weighed media.Bitrate
}

func newPacing(policy media.Bitrate) pacing {
	return pacing{policy: policy, at: policy, machine: rateMachine()}
}

func (p *pacing) onTrial() bool { return p.machine.Is(rateTrial) }

// sinceChange is how long the rate has stood; a rate that never changed has stood for good.
func (p *pacing) sinceChange(now time.Time) time.Duration {
	if p.since.IsZero() {
		return math.MaxInt64
	}
	return now.Sub(p.since)
}

// weighs reads a rate of zero as no bound, for comparing.
func weighs(r media.Bitrate) media.Bitrate {
	if r == 0 {
		return math.MaxInt64
	}
	return r
}

func (p *pacing) wait() time.Duration {
	if p.machine.Is(rateProven) {
		return climbAfter
	}
	return liftAfter
}

// lighten is the lighter rate worth sending a link that took h's intake; a heavier rate on trial is given back instead.
func (p *pacing) lighten(h Vitals) (change, bool) {
	if p.onTrial() {
		return change{move: rateGivenBack, to: p.before}, true
	}
	took := h.Intake()
	if took == 0 {
		return change{}, false
	}
	target := media.Bitrate(float64(took)*linkShare) / step * step
	// The link fell short of p.at, so the target is lighter whatever a mixture of rates says.
	if p.at != 0 && target >= p.at {
		target = p.at * 3 / 4 / step * step
	}
	if target < lowest {
		return change{}, false
	}
	return change{move: rateLightened, to: target, weighed: media.Bitrate(h.weight * 8)}, true
}

// lift is the heavier rate to try on a device that ran well; why says what leaves no room.
func (p *pacing) lift(now time.Time) (c change, ok bool, why string) {
	switch {
	case p.at == 0:
		return change{}, false, "the cast already weighs what the source does"
	case p.onTrial():
		return change{}, false, "a heavier rate is on trial"
	}
	target := media.Bitrate(float64(p.at)*liftBy) / step * step
	known := p.bad != 0 && now.Before(p.until)
	if known {
		// Between what it keeps up with and what it does not.
		target = (p.at + p.bad) / 2 / step * step
	}
	if p.policy != 0 {
		target = min(target, p.policy)
	}
	if p.full != 0 && target >= p.full {
		target = p.policy
	}
	switch {
	case target == p.at || weighs(target) < weighs(p.at) || known && target < p.at+step:
		return change{}, false, noRoom
	case known && weighs(target) >= weighs(p.bad):
		return change{}, false, fmt.Sprintf("%s did not hold lately", p.bad)
	}
	return change{move: rateLifted, to: target}, true, ""
}

// commit makes c the rate asked, once the rate machine allows its move.
func (p *pacing) commit(ctx context.Context, c change, now time.Time) error {
	if err := p.machine.Fire(ctx, c.move); err != nil {
		return err
	}
	switch c.move {
	case rateLifted:
		p.before = p.at
	case rateLightened:
		if p.at == 0 {
			p.full = cmp.Or(p.full, c.weighed)
		}
		p.bad, p.until = p.at, now.Add(p.backoffOr())
	case rateGivenBack:
		p.backoff = min(2*p.backoffOr(), avoidMost)
		p.bad, p.until = p.at, now.Add(p.backoff)
	}
	p.at, p.since = c.to, now
	return nil
}

func (p *pacing) backoffOr() time.Duration {
	if p.backoff == 0 {
		return avoid
	}
	return p.backoff
}

// settled lets a heavier rate on trial stand once it has held for long enough.
func (p *pacing) settled(ctx context.Context, now time.Time) error {
	if !p.onTrial() || p.sinceChange(now) < probeFor {
		return nil
	}
	p.backoff = 0
	return p.machine.Fire(ctx, rateHeld)
}

// refused holds the rate where it is, and the watch off it for a while.
func (p *pacing) refused(now time.Time) { p.since = now }
