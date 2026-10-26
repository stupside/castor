package recovery

import (
	"context"
	"log/slog"

	"github.com/stupside/castor/services/mediaserver/internal/cast/compose"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// change provides everything a strategy needs to decide what to try next.
type change struct {
	intent   Intent
	attempt  Attempt
	outcome  Outcome
	resolver SourceResolver
}

// Action is a concrete change to the next attempt.
type Action string

const (
	SwitchCandidate Action = "switch-candidate"
	DecodeAxis      Action = "decode-axis"
	RelaxRead       Action = "relax-read"
	ServeInstead    Action = "serve-instead"
)

// strategy is one recovery action, applying only where the facts allow it.
type strategy struct {
	action Action
	// apply only ever moves an attempt down a finite order, so recovery ends.
	apply func(context.Context, change) (Attempt, bool)
}

// switchCandidate reads the next link the ranker admitted.
var switchCandidate = strategy{
	action: SwitchCandidate,
	apply: func(ctx context.Context, c change) (Attempt, bool) {
		return nextReadable(ctx, c.intent, c.resolver, c.attempt)
	},
}

// nextReadable resolves the links after a's in rank order, moving past any that cannot be resolved.
func nextReadable(ctx context.Context, in Intent, resolver SourceResolver, a Attempt) (Attempt, bool) {
	return readable(ctx, in, resolver, a, a.candidate+1, len(in.Candidates))
}

// readable is a on the first link from from up to to that resolves.
func readable(ctx context.Context, in Intent, resolver SourceResolver, a Attempt, from, to int) (Attempt, bool) {
	for i := from; i < to; i++ {
		if ctx.Err() != nil {
			return a, false
		}
		link := in.Candidates[i]
		resolved, err := resolver.Resolve(ctx, link, source.Rendition{}, in.Ceiling)
		if err != nil {
			slog.WarnContext(ctx, "a link could not be resolved; moving past it", "host", link.URL.Host, "error", err)
			continue
		}
		a.candidate, a.Stream = i, link
		// Packets the last link broke on do not condemn this one's.
		a.Decode = media.Axes{}
		return a.reading(resolved, in.Deadline), true
	}
	return a, false
}

// decodeAxis decodes the packets the reader died copying.
var decodeAxis = strategy{
	action: DecodeAxis,
	apply: func(_ context.Context, c change) (Attempt, bool) {
		a := c.attempt
		next := a.Decode.Or(c.outcome.Evidence.Copied)
		if next == a.Decode {
			return a, false
		}
		a.Decode = next
		return a, true
	},
}

// relaxRead asks for the same link at playback pace, with no burst for an origin to stall on.
var relaxRead = strategy{
	action: RelaxRead,
	apply: func(_ context.Context, c change) (Attempt, bool) {
		a := c.attempt
		plan, ok := a.Fetch.Cautious()
		if !ok {
			return a, false
		}
		a.Fetch = plan
		return a, true
	},
}

// serveInstead serves a device that refused to fetch the source itself.
var serveInstead = strategy{
	action: ServeInstead,
	apply: func(_ context.Context, c change) (Attempt, bool) {
		a := c.attempt
		// A device already served would refuse the next attempt's identical serve.
		if a.Delivery == compose.DeliveryServe || !c.outcome.Evidence.Handoff {
			return a, false
		}
		a.Delivery = compose.DeliveryServe
		return a, true
	},
}
