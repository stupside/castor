// Package execute runs one attempt of a cast over real processes, servers and devices.
package execute

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/looplab/fsm"
	"github.com/stupside/castor/internal/telemetry"
	"github.com/stupside/castor/services/mediaserver/internal/cast/compose"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

var _ recovery.Runner = Cast{}

// Run casts a end to end; everything it acquired is released before its outcome is read.
func (c Cast) Run(parent context.Context, a recovery.Attempt) (result recovery.Outcome) {
	parent, finish := telemetry.Start(parent, telemetry.Attempt)
	defer func() { finish(result.Err) }()
	if err := a.Program.Validate(); err != nil {
		err = fmt.Errorf("attempt has no valid media program: %w", err)
		return recovery.Outcome{Err: err, Evidence: evidence(ran{}, err, parent.Err() != nil)}
	}
	r, err := newCoordinator(parent, c, a).run()
	return recovery.Outcome{Err: err, Evidence: evidence(r, err, parent.Err() != nil)}
}

// ran is how far an attempt got, and the read it started on the way (nil where it read nothing).
type ran struct {
	reached health.Phase
	reader  *pull
}

// attemptEvent keeps this machine's events distinct at Go call sites.
type attemptEvent uint8

const (
	inspectionStarted attemptEvent = iota
	inspectionFinished
	planFinished
	productionStarted
	presentationReady
	playbackRequested
	playbackStarted
	productionFinished
	playbackEnded
	deliverySettled
	healthEnded
	attemptCancelled
	attemptFailed
	cleanupFinished
)

func (e attemptEvent) String() string {
	return [...]string{
		inspectionStarted:  "inspection-started",
		inspectionFinished: "inspection-finished",
		planFinished:       "plan-finished",
		productionStarted:  "production-started",
		presentationReady:  "presentation-ready",
		playbackRequested:  "playback-requested",
		playbackStarted:    "playback-started",
		productionFinished: "production-finished",
		playbackEnded:      "playback-ended",
		deliverySettled:    "delivery-settled",
		healthEnded:        "health-ended",
		attemptCancelled:   "cancelled",
		attemptFailed:      "failed",
		cleanupFinished:    "cleanup-finished",
	}[e]
}

const (
	preparing        = "preparing"
	inspecting       = "inspecting"
	planning         = "planning"
	outputPlanned    = "planned"
	producing        = "producing"
	ready            = "ready"
	startingPlayback = "starting-playback"
	playingBack      = "playing"
	stopping         = "stopping"
	attemptFinished  = "finished"
)

// Callbacks update state-local evidence. Operations and cleanup remain owned
// by the coordinator outside FSM callbacks.
func (c *coordinator) attemptMachine() *fsm.FSM {
	active := []string{preparing, inspecting, planning, outputPlanned, producing, ready, startingPlayback, playingBack}
	events := fsm.Events{
		{Name: inspectionStarted.String(), Src: []string{preparing}, Dst: inspecting},
		{Name: inspectionFinished.String(), Src: []string{inspecting}, Dst: planning},
		{Name: planFinished.String(), Src: []string{planning}, Dst: outputPlanned},
		{Name: productionStarted.String(), Src: []string{outputPlanned}, Dst: producing},
		{Name: presentationReady.String(), Src: []string{outputPlanned, producing}, Dst: ready},
		{Name: playbackRequested.String(), Src: []string{ready}, Dst: startingPlayback},
		{Name: playbackStarted.String(), Src: []string{startingPlayback}, Dst: playingBack},
		{Name: productionFinished.String(), Src: []string{startingPlayback}, Dst: startingPlayback},
		{Name: productionFinished.String(), Src: []string{playingBack}, Dst: playingBack},
		{Name: playbackEnded.String(), Src: []string{playingBack}, Dst: stopping},
		{Name: deliverySettled.String(), Src: []string{playingBack}, Dst: stopping},
		{Name: healthEnded.String(), Src: []string{playingBack}, Dst: stopping},
		{Name: attemptCancelled.String(), Src: active, Dst: stopping},
		{Name: attemptFailed.String(), Src: active, Dst: stopping},
		{Name: cleanupFinished.String(), Src: []string{stopping}, Dst: attemptFinished},
	}
	track := telemetry.Track("attempt", preparing, attemptFinished)
	return fsm.NewFSM(preparing, events, fsm.Callbacks{
		"after_" + attemptCancelled.String(): func(context.Context, *fsm.Event) { c.interruption = context.Cause(c.ctx) },
		"after_" + playbackEnded.String(): func(context.Context, *fsm.Event) {
			// Capture starvation before cleanup changes the producer's completion.
			if c.presentation.delivery != nil {
				if made, sent, ok := starved(c.presentation.feed, *c.presentation.delivery); ok {
					c.starvation = health.DeviceStarved("the playing cast", made, sent)
				}
			}
		},
		"after_" + playbackStarted.String(): func(context.Context, *fsm.Event) { c.presentation.reached.reached = health.Playing },
		"after_event":                       func(ctx context.Context, e *fsm.Event) { track(ctx, e.Event, e.Src, e.Dst) },
	})
}

func fireAttempt(ctx context.Context, machine *fsm.FSM, event attemptEvent) error {
	// Cancellation is an input to the machine. It must not abort the transition
	// that records stopping or completed cleanup.
	err := machine.Event(context.WithoutCancel(ctx), event.String())
	if stayed, ok := errors.AsType[fsm.NoTransitionError](err); ok {
		return stayed.Err
	}
	return err
}

type operation struct {
	event attemptEvent
	err   error
	done  chan struct{}
}

// coordinator owns one attempt: preparation, state transitions, playback, and
// cleanup. Only its event loop changes state; preparation registers resources
// before handing them back. Operations retain results through cancellation.
type coordinator struct {
	device  Device
	inspect func() (inspectedPresentation, error)
	plan    func(inspectedPresentation) (plannedPresentation, error)
	produce func(plannedPresentation) (presentation, error)

	ctx  context.Context
	stop context.CancelCauseFunc
	// releases undoes everything the attempt acquired, once it ends.
	releases *releases

	machine      *fsm.FSM
	presentation presentation
	inspected    inspectedPresentation
	planned      plannedPresentation
	ready        *readyPresentation
	events       chan *operation
	failures     chan error
	operations   []*operation
	preparation  *operation
	terminal     *operation
	cause        error
	interruption error
	delivered    bool
	starvation   *health.Fault
}

func newCoordinator(parent context.Context, c Cast, a recovery.Attempt) *coordinator {
	ctx, cancel := context.WithCancelCause(parent)
	rel := &releases{ctx: ctx}
	// Signal every owner before joining or releasing any resource.
	rel.interrupt(func() { cancel(nil) })
	s := &coordinator{
		device: c.Device, ctx: ctx, stop: cancel, releases: rel,
		events: make(chan *operation), failures: make(chan error),
	}

	s.machine = s.attemptMachine()

	var preparation *preparation
	s.inspect = func() (inspectedPresentation, error) {
		preparation = newPreparation(ctx, c, a, rel, s.failures)
		return preparation.inspectPresentation()
	}
	s.plan = func(p inspectedPresentation) (plannedPresentation, error) { return preparation.planPresentation(p) }
	s.produce = func(p plannedPresentation) (presentation, error) { return preparation.producePresentation(p) }
	return s
}

func (c *coordinator) launch(event attemptEvent, work func() error) *operation {
	op := &operation{event: event, done: make(chan struct{})}
	c.operations = append(c.operations, op)
	go func() {
		defer close(op.done)
		op.err = work()
		select {
		case c.events <- op:
		case <-c.ctx.Done():
		}
	}()
	return op
}

func (c *coordinator) run() (ran, error) {
	if c.ctx.Err() != nil {
		c.move(attemptCancelled)
		return c.finish()
	}

	c.move(inspectionStarted)
	c.preparation = c.launch(inspectionFinished, func() error {
		var err error
		c.inspected, err = c.inspect()
		c.presentation = c.inspected.presentation
		return err
	})
	c.loop()
	return c.finish()
}

func (c *coordinator) move(event attemptEvent) {
	if err := fireAttempt(c.ctx, c.machine, event); err != nil {
		c.cause = errors.Join(c.cause, err)
		// A rejected transition is an orchestration failure; it still uses the
		// machine's normal stopping path, never SetState.
		if c.machine.Can(attemptFailed.String()) {
			c.cause = errors.Join(c.cause, fireAttempt(c.ctx, c.machine, attemptFailed))
		}
	}
}

func (c *coordinator) loop() {
	for !c.machine.Is(stopping) {
		// Cancellation takes precedence over starting another operation, including
		// when an operation completion was already queued.
		if c.ctx.Err() != nil {
			c.move(attemptCancelled)
			break
		}
		select {
		case <-c.ctx.Done():
			c.move(attemptCancelled)
		case err := <-c.failures:
			c.cause = err
			c.move(attemptFailed)
		case op := <-c.events:
			if c.ctx.Err() != nil {
				c.move(attemptCancelled)
				continue
			}
			if op.err != nil {
				c.move(attemptFailed)
			} else if op.event == presentationReady {
				c.acceptPresentation()
			} else {
				c.move(op.event)
			}
			if c.machine.Is(stopping) {
				c.terminal = op
				continue
			}
			switch op.event {
			case inspectionFinished:
				c.preparation = c.launch(planFinished, func() error {
					var err error
					c.planned, err = c.plan(c.inspected)
					c.presentation = c.planned.presentation
					return err
				})
			case planFinished:
				if c.planned.kind == compose.Handoff {
					c.acceptPresentation()
					c.startPlayback()
				} else {
					c.move(productionStarted)
					if c.machine.Is(producing) {
						c.preparation = c.launch(presentationReady, func() error {
							var err error
							c.presentation, err = c.produce(c.planned)
							return err
						})
					}
				}
			case presentationReady:
				c.startPlayback()
			case playbackStarted:
				c.observePlayback()
			}
		}
	}
}

func (c *coordinator) acceptPresentation() {
	ready, err := certifyPresentation(c.presentation)
	if err != nil {
		c.cause = errors.Join(c.cause, err)
		c.move(attemptFailed)
		return
	}
	c.ready = &ready
	c.move(presentationReady)
}

func (c *coordinator) startPlayback() {
	if !c.machine.Is(ready) {
		return
	}
	c.observeProduction()
	c.move(playbackRequested)
	if !c.machine.Is(startingPlayback) {
		return
	}
	c.launch(playbackStarted, func() error {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		p := c.ready.presentation
		if p.delivery != nil {
			if err := p.delivery.output.Err(); err != nil {
				return err
			}
		}
		if p.feed.buffered != nil {
			if err := p.feed.buffered.reader.Err(); err != nil {
				return err
			}
		}
		return c.ready.play(c.ctx, c.device)
	})
}

func (c *coordinator) observeProduction() {
	if d := c.presentation.delivery; d != nil {
		c.launch(productionFinished, func() error {
			select {
			case <-d.output.Done():
				return d.output.Err()
			case <-c.ctx.Done():
				return nil
			}
		})
		if f := c.presentation.feed; f.buffered != nil && d.output != f.buffered.reader {
			c.launch(productionFinished, func() error {
				select {
				case <-f.buffered.reader.Done():
					return f.buffered.reader.Err()
				case <-c.ctx.Done():
					return nil
				}
			})
		}
	}
}

func (c *coordinator) observePlayback() {
	c.launch(playbackEnded, func() error { return c.device.AwaitEnd(c.ctx) })
	if d := c.presentation.delivery; d != nil {
		c.launch(deliverySettled, func() error { return d.sink.Wait(c.ctx, d.output.Done()) })
		if aud := d.sink.Audience(); aud != nil {
			monitor := playingMonitor(c.presentation.feed, *d, aud)
			c.launch(healthEnded, func() error { return observeHealth(c.ctx, monitor, c.device) })
		}
	}
}

// hand points the device at target; source says whether that is the source itself rather than what castor serves.
func hand(ctx context.Context, dev Device, target *url.URL, contentType string, shown []media.Caption, source bool) (err error) {
	ctx, finish := telemetry.Start(ctx, telemetry.Play)
	defer func() { finish(err) }()
	slog.InfoContext(ctx, "starting playback", "host", target.Host, "content_type", contentType, "handoff", source)
	if err := dev.Play(ctx, target, contentType, shown); err != nil {
		return &playRefused{err: err, source: source}
	}
	return nil
}

// playRefused is the device's own refusal of the URL it was handed.
type playRefused struct {
	err    error
	source bool
}

func (e *playRefused) Error() string { return "starting playback: " + e.err.Error() }
func (e *playRefused) Unwrap() error { return e.err }
