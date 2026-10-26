// Package health judges whether a cast is working: the rules, and the loop that applies them.
package health

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/looplab/fsm"
	"github.com/stupside/castor/internal/telemetry"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Decision asks the attempt to control playback, or says observation completed.
type Decision uint8

const (
	Complete Decision = iota
	Pause
	Resume
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
// One attempt calls Next, applies its decision, and reports the result with Applied.
type Observer struct {
	tracker
	polled bool
	rule   rule
}

func Observe(m Monitor) *Observer {
	now := time.Now()
	o := &Observer{m: m, start: now, grew: now, kept: now}
	o.playback = o.playbackMachine()
	return o
}

func (o *Observer) Next(ctx context.Context) (Decision, error) {
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
		h, err := t.read()
		if err != nil {
			return Complete, err
		}
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
		default:
			t.report(ctx, r, h)
		}
	}
}

// Applied records what actually happened, so a refused pause never counts as held.
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
	// Command cancellation must not erase the outcome of a command already sent.
	previous := o.playback.Current()
	err := o.playback.Event(context.WithoutCancel(ctx), event.String())
	if stayed, ok := errors.AsType[fsm.NoTransitionError](err); ok {
		err = stayed.Err
	}
	if err != nil {
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

// Fault is a verdict that ended a watch; recovery keys on its Kind.
type Fault struct {
	Kind Kind
	// Revise is a verdict reached before the device held the URL, so the attempt may change.
	Revise bool

	// Subject is what was watched (gate, output, or cast).
	Subject string

	// Vitals is the measurements the verdict was reached on.
	Vitals Vitals

	// Err is the producer's terminal error (so errors.Is still finds cancellation).
	Err error

	// Evidence is what the producer printed (retained even if castor killed it).
	Evidence []string
}

// DeviceStarved is the fault of a device that ended on its own while the read behind it still ran, having taken everything it had made.
func DeviceStarved(subject string, made media.Progress, handed int64) *Fault {
	return &Fault{
		Kind:    Starved,
		Subject: subject,
		// It took everything made, so it played everything made.
		Vitals: Vitals{Landed: made.Bytes, Position: made.Position, played: made.Position, handed: handed},
	}
}

func (f *Fault) Error() string {
	if f.Err != nil {
		return fmt.Sprintf("%s: %s: %s", f.Subject, f.Kind, f.Err)
	}
	return fmt.Sprintf("%s: %s", f.Subject, f.Kind)
}

func (f *Fault) Unwrap() error { return f.Err }

// Playback control has its own state machine. It records command outcomes only;
// it owns no goroutines, device connection, cancellation or resource cleanup.
const (
	playbackRunning  = "playing"
	playbackPaused   = "paused"
	resumeRefused    = "resume-refused"
	pauseUnavailable = "pause-unavailable"
)

func (o *Observer) playbackMachine() *fsm.FSM {
	track := telemetry.Track("health", playbackRunning)
	return fsm.NewFSM(playbackRunning, fsm.Events{
		{Name: pauseAccepted.String(), Src: []string{playbackRunning}, Dst: playbackPaused},
		{Name: pauseRejected.String(), Src: []string{playbackRunning}, Dst: pauseUnavailable},
		{Name: resumeAccepted.String(), Src: []string{playbackPaused, resumeRefused}, Dst: playbackRunning},
		{Name: resumeRejected.String(), Src: []string{playbackPaused, resumeRefused}, Dst: resumeRefused},
	}, fsm.Callbacks{
		"after_" + pauseAccepted.String(): func(ctx context.Context, _ *fsm.Event) {
			o.pausedAt = time.Now()
			slog.InfoContext(ctx, "device held", "gate", o.m.Subject, "rule", o.rule.name)
		},
		"after_" + resumeAccepted.String(): func(ctx context.Context, _ *fsm.Event) {
			held := time.Since(o.pausedAt)
			o.held += held
			o.pausedAt = time.Time{}
			slog.InfoContext(ctx, "device released", "gate", o.m.Subject, "rule", o.rule.name, "held", held.Round(time.Millisecond))
		},
		"after_event": func(ctx context.Context, e *fsm.Event) { track(ctx, e.Event, e.Src, e.Dst) },
	})
}

// tracker is one Watch's derived state: how long each thing has been true.
type tracker struct {
	m     Monitor
	start time.Time

	// landed is the artifact's size, and grew when the producer last grew.
	landed int64
	grew   time.Time

	// trail is the media position over the last rateSpan, oldest first.
	trail []mark

	// kept is when the rate last kept up with playback.
	kept time.Time

	// samples counts the speeds the producer stated, not the N/A it prints before.
	sample  media.Progress
	samples int

	// pausedAt is when the device was last held, zero while it plays; held is every hold before it.
	pausedAt time.Time
	held     time.Duration
	playback *fsm.FSM

	reported time.Time
}

// read takes one reading of every port.
func (t *tracker) read() (Vitals, error) {
	now := time.Now()
	h := Vitals{subtitles: t.m.Lead != nil, holdable: t.m.Holdable && !t.playback.Is(pauseUnavailable)}

	if t.m.Landed != nil {
		var err error
		h.Landed, err = t.m.Landed()
		if err != nil {
			return h, err
		}
	}

	if l := t.m.Lead; l != nil {
		h.lead, h.leadDone = l.LatestEnd(), l.Done()
	}

	if tm := t.m.Telemetry; tm != nil {
		sample := tm.Progress()
		if sample != t.sample && sample.Speed > 0 {
			t.samples++
		}
		t.sample = sample
		h.Position, h.Samples = sample.Position, t.samples
		h.rate = t.pace(now, h.Position)
		if since := now.Sub(t.start); since > 0 {
			h.fill = h.Position.Seconds() / since.Seconds()
		}
	}
	if h.paced() {
		t.kept = now
	}
	h.sinceDeficit = now.Sub(t.kept)

	// Growth is media at a pace: a muxer pads bytes with no media behind them, and a trickle never goes silent.
	grew := h.Landed != t.landed
	if t.m.Telemetry != nil {
		grew = h.rate >= minPace
	}
	if grew {
		t.landed, t.grew = h.Landed, now
	}
	h.sinceGrowth = now.Sub(t.grew)

	if p := t.m.Producer; p != nil {
		select {
		case <-p.Done():
			h.ended = true
			// A terminal error is published with Done, never before.
			if tm := t.m.Telemetry; tm != nil {
				h.failed = tm.Err() != nil
			}
		default:
		}
	}

	if a := t.m.Audience; a != nil {
		handed, last := a.Handed()
		h.handed = handed
		// A device that fetched nothing yet is measured from the watch's start, not from the epoch.
		h.sinceFetch = now.Sub(cmp.Or(last, t.start))
	}
	// Only a device plays; the watch starts as Play returns, which understates the media on hand.
	if t.m.Phase == Playing {
		h.paused = t.playback.Is(playbackPaused) || t.playback.Is(resumeRefused)
		held := t.held
		if h.paused {
			h.sincePause = now.Sub(t.pausedAt)
			held += h.sincePause
		}
		h.played, h.held = now.Sub(t.start)-held, held
	}

	if t.m.Grace > 0 {
		h.overdue = now.Sub(t.start) > t.m.Grace
	}

	return h, nil
}

// mark is the media position read at one instant.
type mark struct {
	at       time.Time
	position time.Duration
}

// pace records position and reports the media made per second over the trail, zero until it spans two readings.
func (t *tracker) pace(now time.Time, position time.Duration) float64 {
	t.trail = append(t.trail, mark{at: now, position: position})
	for len(t.trail) > 1 && now.Sub(t.trail[1].at) >= rateSpan {
		t.trail = t.trail[1:]
	}
	oldest := t.trail[0]
	if span := now.Sub(oldest.at); span > 0 {
		return float64(position-oldest.position) / float64(span)
	}
	return 0
}

// fault ends the watch with the producer's own error and evidence; only an abandon is logged, a revise is the recovery's to say.
func (t *tracker) fault(ctx context.Context, r rule, act action, h Vitals) error {
	f := &Fault{
		Kind:    r.kind,
		Revise:  act == revise,
		Subject: t.m.Subject,
		Vitals:  h,
	}
	if r.blames == theProducer && t.m.Producer != nil {
		if tm := t.m.Telemetry; tm != nil {
			f.Err = tm.Err()
		}
		f.Evidence = t.m.Producer.Evidence()
	}
	if act == abandon {
		slog.WarnContext(ctx, "cast abandoned", "gate", t.m.Subject, "verdict", r.kind.String(), "rule", r.name)
	}
	return f
}

// report says what the watch is waiting on.
func (t *tracker) report(ctx context.Context, r rule, h Vitals) {
	if time.Since(t.reported) < reportInterval {
		return
	}
	t.reported = time.Now()
	slog.DebugContext(ctx, "watch",
		"gate", t.m.Subject,
		"rule", r.name,
		"media_position", h.Position.Round(time.Second),
		"ahead", h.ahead().Round(time.Second),
		"paused", h.paused,
	)
}
