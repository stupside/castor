package health

import (
	"context"
	"time"
)

const headEvery = 5 * time.Second

// Playhead is where a device says it has played to, and whether its viewer holds it there.
type Playhead interface {
	Position(ctx context.Context) (at time.Duration, paused, ok bool)
}

// playTrail is what a device last said of its own playhead.
type playTrail struct {
	source Playhead
	polled time.Time
	// last is the latest position the device said, known once it said one; held is that its viewer held it there.
	last      time.Duration
	lastKnown bool
	held      bool
}

func newPlayTrail(source Playhead) playTrail { return playTrail{source: source} }

// poll asks the device where it is, once every headEvery.
func (p *playTrail) poll(ctx context.Context, now time.Time) {
	if p.source == nil || now.Sub(p.polled) < headEvery {
		return
	}
	p.polled = now
	ctx, cancel := context.WithTimeout(ctx, headEvery)
	defer cancel()
	if position, held, ok := p.source.Position(ctx); ok {
		p.last, p.lastKnown, p.held = position, true, held
	}
}
