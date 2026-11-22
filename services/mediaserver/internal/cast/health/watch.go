// Package health judges whether a cast is working: the rules, and the loop that applies them.
package health

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/stupside/castor/internal/machine"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Decision asks the attempt to control playback, or says observation completed.
type Decision uint8

const (
	Complete Decision = iota
	Pause
	Resume
	// ChangeRate asks the cast to weigh what Target says while the device plays on.
	ChangeRate
)

// controlEvent keeps this machine's events distinct at Go call sites.
type controlEvent uint8

const (
	pauseAccepted controlEvent = iota
	pauseRejected
	resumeAccepted
	resumeRejected
)

func (e controlEvent) String() string {
	return [...]string{
		pauseAccepted:  "pause-accepted",
		pauseRejected:  "pause-rejected",
		resumeAccepted: "resume-accepted",
		resumeRejected: "resume-rejected",
	}[e]
}

// Observer retains health measurements and policy state. It never controls a device.
// One attempt calls Next, applies its decision, and reports the result with Applied or Rated.
type Observer struct {
	tracker
	polled bool
	rule   rule
	// pending is the change of rate the last ChangeRate asked for.
	pending change
	// floored is a link too poor to send a cast over at any rate worth sending, said once.
	floored bool
	// said is why a cast was last found not lifted, so it is said once for as long as it holds.
	said whyNotLifted
	// last is the latest reading, read once the watch is over.
	last Vitals
}

func Observe(m Monitor) *Observer {
	now := time.Now()
	o := &Observer{m: m, start: now, grew: now, kept: now, made: now}
	o.trail = media.Trail{Span: rateSpan}
	o.reading = newReading(m.Playhead)
	o.asked = newPacing(m.MaxBitrate, m.Asked)
	o.playback = o.playbackMachine()
	return o
}

func (o *Observer) Next(ctx context.Context) (d Decision, err error) {
	defer func() {
		if d == Complete {
			// The watch is over, and its machines with it.
			o.playback.End(ctx)
			o.asked.machine.End(ctx)
		}
	}()
	t := &o.tracker
	for {
		if err := context.Cause(ctx); err != nil {
			return Complete, err
		}
		if o.polled {
			select {
			case <-ctx.Done():
				return Complete, context.Cause(ctx)
			case <-time.After(pollInterval):
			}
		}
		o.polled = true
		h, err := t.read(ctx)
		if err != nil {
			return Complete, err
		}
		o.last = h
		r, act, err := judge(t.m.Phase, h)
		if err != nil {
			return Complete, err
		}
		o.rule = r
		switch act {
		case open:
			slog.InfoContext(ctx, "watch cleared", "gate", t.m.Subject, "rule", r.name, "waited", time.Since(t.start).Round(time.Millisecond))
			return Complete, nil
		case revise, abandon:
			return Complete, t.fault(ctx, r, act, h)
		case hold:
			return Pause, nil
		case release:
			return Resume, nil
		case lighten:
			if c, ok := o.asked.lighten(h); ok {
				o.pending, o.floored = c, false
				return ChangeRate, nil
			}
			if !o.floored {
				o.floored = true
				slog.WarnContext(ctx, "the device's link cannot carry a cast at a rate worth sending; it is left to play it", "gate", t.m.Subject, "took", h.Intake().String())
			}
		case lift:
			if c, ok, _ := o.asked.lift(time.Now()); ok {
				o.pending = c
				return ChangeRate, nil
			}
		default:
			if err := o.asked.settled(ctx, time.Now()); err != nil {
				return Complete, err
			}
			o.explain(ctx, h)
			t.report(ctx, r, h)
		}
	}
}

// explain logs, once per reason, why a cast lighter than it could be is not asked for more.
func (o *Observer) explain(ctx context.Context, h Vitals) {
	if o.asked.at == 0 || o.asked.onTrial() || h.sinceChange < o.asked.wait() || o.asked.policy != 0 && o.asked.at >= o.asked.policy {
		return
	}
	blocked := h.unlifted()
	if _, _, refused := o.asked.lift(time.Now()); blocked.why == noRoom && refused.why != "" {
		blocked = refused
	}
	if blocked.why == "" || blocked.why == o.said {
		return
	}
	o.said = blocked.why
	attrs := []any{"gate", o.m.Subject, "why", blocked.why, "at", o.asked.at.String()}
	if blocked.detail != "" {
		attrs = append(attrs, "detail", blocked.detail)
	}
	slog.InfoContext(ctx, "the cast is not asked for more", attrs...)
}

// Latest is the last reading, which says where the viewer got to when something else ended the cast.
func (o *Observer) Latest() Vitals { return o.last }

// Target is the rate a ChangeRate asks the cast to weigh, zero for as the source does.
func (o *Observer) Target() media.Bitrate { return o.pending.to }

// Applied records how a Pause or Resume went, so a refused pause never counts as held.
func (o *Observer) Applied(ctx context.Context, decision Decision, commandErr error) error {
	var event controlEvent
	switch decision {
	case Pause:
		event = pauseAccepted
		if commandErr != nil {
			event = pauseRejected
		}
	case Resume:
		event = resumeAccepted
		if commandErr != nil {
			event = resumeRejected
		}
	default:
		return fmt.Errorf("health: unexpected control result %d", decision)
	}
	previous := o.playback.State()
	if err := o.playback.Fire(ctx, event); err != nil {
		return err
	}
	if commandErr != nil {
		switch decision {
		case Pause:
			slog.WarnContext(ctx, "the device would not pause, so it is left to play out what it has", "gate", o.m.Subject, "rule", o.rule.name, "error", commandErr)
		case Resume:
			if previous != resumeRefused {
				slog.WarnContext(ctx, "the device would not resume", "gate", o.m.Subject, "rule", o.rule.name, "error", commandErr)
			}
		}
	}
	return nil
}

// Rated records how the ChangeRate went: a refused change leaves the rate as it was, a made one begins after byte cut.
func (o *Observer) Rated(ctx context.Context, cut int64, switchErr error) error {
	c, now := o.pending, time.Now()
	if switchErr != nil {
		o.asked.refused(now)
		slog.WarnContext(ctx, "the cast's rate could not be changed", "gate", o.m.Subject, "move", c.move.String(), "to", c.to.String(), "error", switchErr)
		return nil
	}
	was := o.asked.at
	if err := o.asked.commit(ctx, c, now); err != nil {
		return err
	}
	// What was measured and said of the device was of the rate it left.
	o.reading.restart()
	o.said = ""
	if cut > 0 {
		o.curve = slices.DeleteFunc(o.curve, func(m made) bool { return m.bytes > cut })
	}
	slog.InfoContext(ctx, "the cast's rate changed for the device's link", "gate", o.m.Subject, "move", c.move.String(), "from", was.String(), "to", c.to.String())
	return nil
}

// playbackState is castor's control of the device's playback. It records command outcomes only;
// it owns no goroutines, device connection, cancellation or resource cleanup.
type playbackState uint8

const (
	playbackRunning playbackState = iota
	playbackPaused
	resumeRefused
	pauseUnavailable
)

func (s playbackState) String() string {
	return [...]string{
		playbackRunning:  "playing",
		playbackPaused:   "paused",
		resumeRefused:    "resume-refused",
		pauseUnavailable: "pause-unavailable",
	}[s]
}

func (o *Observer) playbackMachine() *machine.Machine[playbackState, controlEvent] {
	return machine.New("playback", playbackRunning, nil, []machine.Edge[playbackState, controlEvent]{
		{On: pauseAccepted, From: []playbackState{playbackRunning}, To: playbackPaused},
		{On: pauseRejected, From: []playbackState{playbackRunning}, To: pauseUnavailable},
		{On: resumeAccepted, From: []playbackState{playbackPaused, resumeRefused}, To: playbackRunning},
		{On: resumeRejected, From: []playbackState{playbackPaused, resumeRefused}, To: resumeRefused},
	}, map[controlEvent]func(context.Context){
		pauseAccepted: func(ctx context.Context) {
			o.pausedAt = time.Now()
			slog.InfoContext(ctx, "device held", "gate", o.m.Subject, "rule", o.rule.name)
		},
		resumeAccepted: func(ctx context.Context) {
			held := time.Since(o.pausedAt)
			o.held += held
			o.pausedAt = time.Time{}
			slog.InfoContext(ctx, "device released", "gate", o.m.Subject, "rule", o.rule.name, "held", held.Round(time.Millisecond))
		},
	})
}
