package health

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestTheWatchEndsOnAVerdictCarryingTheProducersError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sentinel := errors.New("upstream pull: exit status 183")
		producer := &fakeProducer{done: make(chan struct{}), err: sentinel, evidence: []string{"HTTP error 404 Not Found"}}
		close(producer.done)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		err := watchFixture(ctx, Monitor{
			Subject: "playback gate", Phase: Reading,
			Producer: producer, Telemetry: producer,
			Landed: func() (int64, error) { return 1, nil },
		})
		fault, ok := errors.AsType[*Fault](err)
		if !ok || fault.Kind != Dead || !fault.Revise || !errors.Is(err, sentinel) || len(fault.Evidence) != 1 {
			t.Fatalf("Watch = %v, want a revisable dead verdict carrying the reader's error and stderr", err)
		}
	})
}

// fakeProducer is a read that has already settled.
type fakeProducer struct {
	done     chan struct{}
	err      error
	evidence []string
}

func (f *fakeProducer) Progress() media.Progress { return media.Progress{} }
func (f *fakeProducer) Done() <-chan struct{}    { return f.done }
func (f *fakeProducer) Err() error               { return f.err }
func (f *fakeProducer) Evidence() []string       { return f.evidence }

func TestBytesWithNoMediaBehindThemAreAStall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// A dead live edge: the muxer keeps writing tables while the media position never moves.
		producer := &frozenProducer{done: make(chan struct{}), position: 11 * time.Second}
		var landed int64
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		defer cancel()
		err := watchFixture(ctx, Monitor{
			Subject: "the playing cast", Phase: Playing,
			Producer: producer, Telemetry: producer,
			Landed: func() (int64, error) { landed += 188; return landed, nil },
		})
		if fault, ok := errors.AsType[*Fault](err); !ok || fault.Kind != Stalled {
			t.Fatalf("Watch = %v, want a stall once no media arrived for the stall window", err)
		}
	})
}

// frozenProducer is a read whose media position stopped while it keeps running.
type frozenProducer struct {
	done     chan struct{}
	position time.Duration
}

func (f *frozenProducer) Progress() media.Progress {
	return media.Progress{Position: f.position, Speed: 1}
}
func (f *frozenProducer) Done() <-chan struct{} { return f.done }
func (f *frozenProducer) Err() error            { return nil }
func (f *frozenProducer) Evidence() []string    { return nil }

// A trickle never goes silent, so only growth judged against playback pace sees it stop.
func TestATrickleUnderHalfOfPlaybackIsAStall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		producer := &pacedProducer{done: make(chan struct{}), start: time.Now(), made: func(d time.Duration) time.Duration { return d / time.Minute * time.Second }}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		defer cancel()
		err := watchFixture(ctx, Monitor{Subject: "the playing cast", Phase: Playing, Producer: producer, Telemetry: producer})
		if fault, ok := errors.AsType[*Fault](err); !ok || fault.Kind != Stalled {
			t.Fatalf("Watch = %v, want a stall from a producer making a second of media a minute", err)
		}
	})
}

// A producer slower than playback but past half of it is still delivering, whatever it lost before.
func TestAProducerKeepingHalfOfPlaybackIsNotAStall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		producer := &pacedProducer{done: make(chan struct{}), start: time.Now(), made: func(d time.Duration) time.Duration { return d * 6 / 10 }}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		defer cancel()
		err := watchFixture(ctx, Monitor{Subject: "the playing cast", Phase: Playing, Producer: producer, Telemetry: producer})
		if _, judged := errors.AsType[*Fault](err); judged {
			t.Fatalf("Watch = %v, want no verdict against a producer at 0.6x", err)
		}
	})
}

// pacedProducer makes media as a function of the time since it started.
type pacedProducer struct {
	done  chan struct{}
	start time.Time
	made  func(time.Duration) time.Duration
}

func (p *pacedProducer) Progress() media.Progress {
	return media.Progress{Position: p.made(time.Since(p.start)), Speed: 1}
}
func (p *pacedProducer) Done() <-chan struct{} { return p.done }
func (p *pacedProducer) Err() error            { return nil }
func (p *pacedProducer) Evidence() []string    { return nil }

// A read too slow to land even the cushion is given up before any device is pointed at it.
func TestAReadThatCannotLandItsCushionIsUndeliverable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		producer := &pacedProducer{done: make(chan struct{}), start: time.Now(), made: func(d time.Duration) time.Duration { return d * 2 / 10 }}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		defer cancel()
		err := watchFixture(ctx, Monitor{Subject: "playback gate", Phase: Reading, Producer: producer, Telemetry: producer, Landed: func() (int64, error) { return 1 << 20, nil }})
		fault, ok := errors.AsType[*Fault](err)
		if !ok || fault.Kind != Undeliverable || !fault.Revise {
			t.Fatalf("Watch = %v, want a revisable undeliverable verdict against a 0.2x read", err)
		}
		if waited := time.Since(producer.start); waited > deliverBudget+time.Second {
			t.Errorf("the gate waited %v on a read that never kept pace, want about the deliver budget", waited)
		}
	})
}

// A read at playback pace opens the gate once the cushion is landed.
func TestAReadKeepingPaceOpensOnItsCushion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		producer := &pacedProducer{done: make(chan struct{}), start: time.Now(), made: func(d time.Duration) time.Duration { return d * 12 / 10 }}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		defer cancel()
		if err := watchFixture(ctx, Monitor{Subject: "playback gate", Phase: Reading, Producer: producer, Telemetry: producer, Landed: func() (int64, error) { return 1 << 20, nil }}); err != nil {
			t.Fatalf("Watch = %v, want the gate open", err)
		}
		if waited := time.Since(producer.start); waited > readCushion+time.Second {
			t.Errorf("the gate waited %v, want it open as soon as the cushion landed", waited)
		}
	})
}

// heldDevice records every hold and release, and takes bytes the moment it is asked.
type heldDevice struct {
	mu       sync.Mutex
	holds    []time.Time
	releases []time.Time
	refuse   error
	// refuseResume fails every Resume.
	refuseResume error
}

func (d *heldDevice) Handed() (int64, time.Time) { return 1 << 20, time.Now() }
func (d *heldDevice) Pause(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse != nil {
		return d.refuse
	}
	d.holds = append(d.holds, time.Now())
	return nil
}
func (d *heldDevice) Resume(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuseResume != nil {
		return d.refuseResume
	}
	d.releases = append(d.releases, time.Now())
	return nil
}

// A read that falls behind a playing device has the device held, and released once it is the cushion ahead again.
func TestADeviceOutrunningItsReadIsHeldThenReleased(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		// Ahead by the cushion for a minute, then frozen for a minute, then racing ahead again.
		producer := &pacedProducer{done: make(chan struct{}), start: start, made: func(d time.Duration) time.Duration {
			switch {
			case d < time.Minute:
				return d + readCushion
			case d < 2*time.Minute:
				return time.Minute + readCushion
			default:
				return time.Minute + readCushion + 3*(d-2*time.Minute)
			}
		}}
		dev := &heldDevice{}
		ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
		defer cancel()
		go func() {
			<-ctx.Done()
			close(producer.done)
		}()
		err := watchFixture(ctx, Monitor{Subject: "the playing cast", Phase: Playing, Producer: producer, Telemetry: producer, Audience: dev, Holdable: true}, dev)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Watch = %v, want it still watching a cast that recovered", err)
		}
		dev.mu.Lock()
		defer dev.mu.Unlock()
		if len(dev.holds) != 1 || len(dev.releases) != 1 {
			t.Fatalf("held %d times and released %d, want once each", len(dev.holds), len(dev.releases))
		}
		// Frozen from 1:00 with the cushion ahead, the device reaches the low-water mark 10s later.
		if held := dev.holds[0].Sub(start); held < time.Minute+9*time.Second || held > time.Minute+12*time.Second {
			t.Errorf("held at %v, want about 1m10s when the device came within the low-water mark", held)
		}
		// Racing from 2:00 at 3x, the read is the high-water mark ahead of the device held at 1:10 some 5s later.
		if released := dev.releases[0].Sub(start); released < 2*time.Minute+4*time.Second || released > 2*time.Minute+7*time.Second {
			t.Errorf("released at %v, want about 2m05s when the read was the high-water mark ahead again", released)
		}
	})
}

// A device held for the whole pause budget with the read still behind is starved.
func TestADeviceHeldPastThePauseBudgetIsStarved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		producer := &frozenProducer{done: make(chan struct{}), position: 5 * time.Second}
		dev := &heldDevice{}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		defer cancel()
		err := watchFixture(ctx, Monitor{Subject: "the playing cast", Phase: Playing, Producer: producer, Telemetry: producer, Audience: dev, Holdable: true}, dev)
		fault, ok := errors.AsType[*Fault](err)
		if !ok || fault.Kind != Starved || fault.Revise {
			t.Fatalf("Watch = %v, want a starved verdict after the pause budget", err)
		}
		if fault.Vitals.Position != 5*time.Second || !fault.Vitals.paused {
			t.Errorf("the verdict carries %+v, want the measurements it was reached on", fault.Vitals)
		}
		dev.mu.Lock()
		defer dev.mu.Unlock()
		if len(dev.holds) != 1 || len(dev.releases) != 0 {
			t.Errorf("held %d times and released %d, want held once and never released", len(dev.holds), len(dev.releases))
		}
	})
}

// A device that will not be held is asked once, then judged as one that cannot be held.
func TestADeviceThatRefusesToPauseIsJudgedUnholdable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		producer := &frozenProducer{done: make(chan struct{}), position: 5 * time.Second}
		dev := &heldDevice{refuse: errors.New("UPnP 701: transition not available")}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		defer cancel()
		err := watchFixture(ctx, Monitor{Subject: "the playing cast", Phase: Playing, Producer: producer, Telemetry: producer, Audience: dev, Holdable: true}, dev)
		if fault, ok := errors.AsType[*Fault](err); !ok || fault.Kind != Stalled {
			t.Fatalf("Watch = %v, want a stall once the device played out what the silent read had made", err)
		}
	})
}

// A device that refuses to resume is retried every tick but warned of once.
func TestARefusedResumeIsWarnedOnce(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	dev := &heldDevice{refuseResume: errors.New("UPnP 701")}
	tr := Observe(Monitor{Subject: "the playing cast", Holdable: true})
	if err := tr.Applied(t.Context(), Pause, nil); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := tr.Applied(t.Context(), Resume, dev.Resume(t.Context())); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(logs.String(), "would not resume"); got != 1 {
		t.Errorf("warned %d times over 3 refused resumes, want once", got)
	}
	if tr.pausedAt.IsZero() {
		t.Error("a refused resume released the hold anyway")
	}
}

// watchFixture applies decisions to a fake device; production orchestration lives in execute.
func watchFixture(ctx context.Context, m Monitor, devices ...*heldDevice) error {
	observer := Observe(m)
	for {
		decision, err := observer.Next(ctx)
		if err != nil || decision == Complete {
			return err
		}
		switch decision {
		case Pause:
			err = devices[0].Pause(ctx)
		case Resume:
			err = devices[0].Resume(ctx)
		}
		if err := observer.Applied(ctx, decision, err); err != nil {
			return err
		}
	}
}

func TestPlaybackControlRejectsOutOfOrderResults(t *testing.T) {
	o := Observe(Monitor{Phase: Playing, Holdable: true})
	if err := o.Applied(t.Context(), Resume, nil); err == nil {
		t.Fatal("accepted a resume before a pause")
	}
	if err := o.Applied(t.Context(), Pause, nil); err != nil {
		t.Fatal(err)
	}
	pausedAt := o.pausedAt
	if err := o.Applied(t.Context(), Pause, nil); err == nil {
		t.Fatal("accepted a second pause and reset its timing")
	}
	if o.pausedAt != pausedAt {
		t.Fatal("invalid result changed the pause clock")
	}
}

func TestResumeRetriesPreserveTheWholePause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := Observe(Monitor{Phase: Playing, Holdable: true})
		if err := o.Applied(t.Context(), Pause, nil); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			time.Sleep(time.Second)
			if err := o.Applied(t.Context(), Resume, errors.New("device busy")); err != nil {
				t.Fatal(err)
			}
			h, err := o.read(t.Context())
			if err != nil || !h.paused {
				t.Fatalf("refused resume released playback: %+v, %v", h, err)
			}
		}
		// A command outcome is still recorded if cancellation arrives before it is collected.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := o.Applied(ctx, Resume, nil); err != nil {
			t.Fatal(err)
		}
		h, err := o.read(t.Context())
		if err != nil || h.paused || h.held != 3*time.Second || h.played != 0 {
			t.Fatalf("pause accounting after retries: %+v, %v", h, err)
		}
	})
}

func TestRefusedPauseDisablesFurtherHolds(t *testing.T) {
	o := Observe(Monitor{Phase: Playing, Holdable: true})
	if err := o.Applied(t.Context(), Pause, errors.New("unsupported")); err != nil {
		t.Fatal(err)
	}
	h, err := o.read(t.Context())
	if err != nil || h.paused || h.holdable {
		t.Fatalf("refused pause: %+v, %v", h, err)
	}
	if err := o.Applied(t.Context(), Pause, nil); err == nil {
		t.Fatal("accepted another pause after refusal")
	}
}

// pausingDevice plays from the start and says when its viewer holds it.
type pausingDevice struct {
	mu     sync.Mutex
	at     time.Duration
	since  time.Time
	held   bool
	handed int64
}

func (d *pausingDevice) Handed() (int64, time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.handed, time.Now()
}

func (d *pausingDevice) Position(context.Context) (time.Duration, bool, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.held {
		return d.at, true, true
	}
	return d.at + time.Since(d.since), false, true
}

func (d *pausingDevice) hold(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if on {
		d.at += time.Since(d.since)
	} else {
		d.since = time.Now()
	}
	d.held = on
}

// A device its viewer pauses is neither judged by its stopped playhead nor counted as playing while held.
func TestADevicesOwnPauseIsAHoldNotAStall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		dev := &pausingDevice{since: start, handed: 1 << 20}
		producer := &pacedProducer{done: make(chan struct{}), start: start, made: func(d time.Duration) time.Duration { return d + time.Minute }}
		o := Observe(Monitor{Subject: "the playing cast", Phase: Playing, Producer: producer, Telemetry: producer, Audience: dev, Playhead: dev})
		read := func() Vitals {
			h, err := o.read(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			return h
		}
		for range 40 {
			time.Sleep(time.Second)
			read()
		}
		before := read()
		dev.hold(true)
		for range 60 {
			time.Sleep(time.Second)
			read()
		}
		held := read()
		if !held.viewerHeld || held.played-before.played > 6*time.Second {
			t.Errorf("after a minute's hold by its viewer: held %t, played %v more; want held, the hold not counted as playing", held.viewerHeld, held.played-before.played)
		}
		if held.held != 0 {
			t.Errorf("the viewer's hold counts %v as castor's, which would starve a cast it later holds", held.held)
		}
	})
}
