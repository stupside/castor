package health

import (
	"context"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

const (
	headEvery  = 5 * time.Second
	paceSpan   = 30 * time.Second
	stallBelow = 0.9
	keepsAbove = 0.97
)

// Playhead is where a device says it has played to, and whether its viewer holds it there.
type Playhead interface {
	Position(ctx context.Context) (at time.Duration, paused, ok bool)
}

// playTrail measures the media a device plays per second of wall clock from its own playhead.
type playTrail struct {
	source Playhead
	heads  media.Trail
	polled time.Time
	pace   float64
	paceAt time.Time
	// last is the latest position the device said, known once it said one; held is that its viewer held it there.
	last      time.Duration
	lastKnown bool
	held      bool
	// moving is a playhead seen advancing since the trail last started; before, the device is still opening the stream.
	moving bool
}

func newPlayTrail(source Playhead) playTrail {
	return playTrail{source: source, heads: media.Trail{Span: paceSpan}}
}

// measure is the pace over the last paceSpan, false while it is unmeasured, stale, or the device is held.
func (p *playTrail) measure(ctx context.Context, now time.Time, paused bool) (float64, bool) {
	if p.source == nil {
		return 0, false
	}
	if paused {
		p.restart()
		return 0, false
	}
	if now.Sub(p.polled) >= headEvery {
		p.polled = now
		p.poll(ctx, now)
	}
	if p.held {
		return 0, false
	}
	return p.pace, !p.paceAt.IsZero() && now.Sub(p.paceAt) < paceSpan
}

func (p *playTrail) poll(ctx context.Context, now time.Time) {
	ctx, cancel := context.WithTimeout(ctx, headEvery)
	defer cancel()
	position, held, ok := p.source.Position(ctx)
	if !ok {
		return
	}
	moved := p.lastKnown && position > p.last
	p.last, p.lastKnown, p.held = position, true, held
	switch {
	case held:
		// A playhead its viewer stopped says nothing of the link.
		p.restart()
		return
	case !p.moving && !moved:
		// Nor does the time a device takes to start playing again.
		return
	}
	p.moving = true
	p.heads.Add(now, position.Seconds())
	if pace, known := p.heads.Rate(0.75); known {
		p.pace, p.paceAt = pace, now
	}
}

// restart measures the pace afresh from when the playhead next moves.
func (p *playTrail) restart() {
	p.heads.Reset()
	p.paceAt, p.moving = time.Time{}, false
}

// forget drops what the device last said, as when it asked for the stream from elsewhere.
func (p *playTrail) forget() {
	p.last, p.lastKnown, p.held = 0, false, false
}
