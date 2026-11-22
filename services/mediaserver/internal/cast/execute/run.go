// Package execute runs one attempt of a cast over real processes, servers and devices.
package execute

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/stupside/castor/internal/machine"
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
	out := recovery.Outcome{Err: err, Evidence: evidence(r, err, parent.Err() != nil)}
	if err != nil {
		out.Evidence = withPlace(parent, c.Device, a, out.Evidence)
	}
	return out
}

// ran is how far an attempt got, where its playback began, and the read it started on the way (nil where it read nothing).
type ran struct {
	reached health.Phase
	began   time.Duration
	reader  *pull
	// seen is the playing watch's last reading.
	seen health.Vitals
}

type operation struct {
	event attemptEvent
	err   error
	done  chan struct{}
	// off calls the operation off: once it ends, what the operation reports is not heard.
	off context.Context
}

// coordinator owns one attempt: preparation, state transitions, playback, and
// cleanup. Only its event loop changes state; preparation registers resources
// before handing them back. Operations retain results through cancellation.
type coordinator struct {
	device     Device
	maxBitrate media.Bitrate
	climb      time.Duration
	inspect    func() (inspectedPresentation, error)
	plan       func(inspectedPresentation) (plannedPresentation, error)
	produce    func(plannedPresentation) (presentation, error)

	ctx  context.Context
	stop context.CancelCauseFunc
	// releases undoes everything the attempt acquired, once it ends.
	releases *releases

	machine      *machine.Machine[attemptState, attemptEvent]
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
	// made is the delivery as made since playback began or the device last sought; what watches it is called off with it.
	made       context.Context
	unmade     context.CancelFunc
	stopHealth context.CancelFunc
	stopDrain  context.CancelFunc
	// observer judges the playing cast, nil before it plays.
	observer *health.Observer
}

func newCoordinator(parent context.Context, c Cast, a recovery.Attempt) *coordinator {
	ctx, cancel := context.WithCancelCause(parent)
	rel := &releases{ctx: ctx}
	// Signal every owner before joining or releasing any resource.
	rel.interrupt(func() { cancel(nil) })
	s := &coordinator{
		device: c.Device, maxBitrate: c.MaxBitrate, climb: a.Climb, ctx: ctx, stop: cancel, releases: rel,
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
	return c.start(&operation{event: event}, work)
}

// launchUntil is launch for work off calls off, which then has nothing to say however it ended.
func (c *coordinator) launchUntil(off context.Context, event attemptEvent, work func() error) *operation {
	return c.start(&operation{event: event, off: off}, func() error {
		if err := work(); off.Err() == nil || c.ctx.Err() != nil {
			return err
		}
		return nil
	})
}

func (c *coordinator) start(op *operation, work func() error) *operation {
	op.done = make(chan struct{})
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

// move is an event the attempt takes; one its state refuses is an orchestration failure that stops it the machine's own way.
func (c *coordinator) move(event attemptEvent) {
	c.cause = errors.Join(c.cause, c.machine.FireOrFail(c.ctx, event, attemptFailed))
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
			c.cause = errors.Join(c.cause, err)
			c.move(attemptFailed)
		case op := <-c.events:
			if c.ctx.Err() != nil {
				c.move(attemptCancelled)
				continue
			}
			if op.off != nil && op.off.Err() != nil {
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
			case deliverySettled:
				c.drain()
			case deliverySought:
				c.sought()
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
	c.watchProduction()
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

// watchProduction watches what makes the delivery anew, until it ends or the device seeks.
func (c *coordinator) watchProduction() {
	d := c.presentation.delivery
	if d == nil {
		return
	}
	c.made, c.unmade = context.WithCancel(c.ctx)
	made := c.made
	c.launchUntil(made, productionFinished, func() error { return ended(made, d.output) })
	if f := c.presentation.feed; f.buffered != nil && d.output != f.buffered.reader {
		c.launchUntil(made, productionFinished, func() error { return ended(made, f.buffered.reader) })
	}
}

// ended says how what makes p ended, nothing where made ended first.
func ended(made context.Context, p producer) error {
	if madeDone(made, p) == nil {
		return nil
	}
	return p.Err()
}

// madeDone is p's Done once what makes p ended, followed through changes of rate or rung; nil once made ends first.
func madeDone(made context.Context, p producer) <-chan struct{} {
	for done := p.Done(); ; done = p.Done() {
		select {
		case <-done:
			if p.Done() == done {
				return done
			}
		case <-made.Done():
			return nil
		}
	}
}

func (c *coordinator) observePlayback() {
	c.launch(playbackEnded, func() error { return c.device.AwaitEnd(c.ctx) })
	if d := c.presentation.delivery; d != nil {
		c.settle(d)
		c.watchHealth(d)
		c.watchSeeks(d)
	}
}

// settle waits for the device to take the whole of what is made, once it is made.
func (c *coordinator) settle(d *delivery) {
	made := c.made
	produced := make(chan struct{})
	go func() {
		if madeDone(made, d.output) != nil {
			close(produced)
		}
	}()
	c.launchUntil(made, deliverySettled, func() error { return d.sink.Wait(made, produced) })
}

// watchHealth judges the playing cast until nothing is left to judge.
func (c *coordinator) watchHealth(d *delivery) {
	aud := d.sink.Audience()
	if aud == nil {
		return
	}
	observer := health.Observe(playingMonitor(c.presentation.feed, *d, aud, c.maxBitrate, c.climb, c.device))
	c.observer = observer
	watching, stop := context.WithCancel(c.ctx)
	c.stopHealth = stop
	c.launchUntil(watching, healthEnded, func() error { return wedgedOn(observeHealth(watching, observer, c.device, d.rate), *d) })
}

func (c *coordinator) watchSeeks(d *delivery) {
	if d.sought == nil {
		return
	}
	c.launch(deliverySought, func() error {
		select {
		case <-d.sought:
		case <-c.ctx.Done():
		}
		return nil
	})
}

// sought watches the delivery made anew from where the device asked, the device being no longer let play out what it held.
func (c *coordinator) sought() {
	d := c.presentation.delivery
	c.unmade()
	c.watchProduction()
	c.settle(d)
	if c.stopDrain != nil {
		c.stopDrain()
		c.stopDrain = nil
		c.watchHealth(d)
	}
	c.watchSeeks(d)
}

const (
	// drainGrace is how long past the media it holds a device that took the whole stream is given to say it ended.
	drainGrace = 30 * time.Second
	// drainMost bounds that wait for a device that cannot say where it is.
	drainMost = 5 * time.Minute
	askFor    = 5 * time.Second
)

// drain waits for a device that took the whole stream to say it played it out; one that never says is let go once that media has had time to play.
func (c *coordinator) drain() {
	if c.stopHealth != nil {
		c.stopHealth()
	}
	d := c.presentation.delivery
	waiting, stop := context.WithCancel(c.ctx)
	c.stopDrain = stop
	c.launchUntil(waiting, drainElapsed, func() error {
		// Castor held the device only while its read lagged, and that read is over.
		if err := c.device.Resume(waiting); err != nil {
			slog.WarnContext(waiting, "the device castor held would not resume", "error", err)
		}
		left, _, _ := c.leftToPlay(waiting, d)
		slog.InfoContext(waiting, "the device took the whole stream and plays out what it holds", "left", left.Round(time.Second))
		for {
			select {
			case <-time.After(left + drainGrace):
			case <-waiting.Done():
				return nil
			}
			// A paused device, or one still moving through what it holds, has not played out yet.
			next, held, known := c.leftToPlay(waiting, d)
			if !known || !held && (next == 0 || next >= left) {
				slog.WarnContext(waiting, "the device never said it ended; the cast ends with the media it held")
				return nil
			}
			left = next
		}
	})
}

// leftToPlay is what the device has yet to play of d, and whether it is paused.
func (c *coordinator) leftToPlay(ctx context.Context, d *delivery) (left time.Duration, held, known bool) {
	at, held, known := askPosition(ctx, c.device)
	if !known {
		return drainMost, false, false
	}
	return min(drainMost, max(0, d.output.Progress().Position-at)), held, true
}
