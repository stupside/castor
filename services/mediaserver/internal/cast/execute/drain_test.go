package execute

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// settling is a delivery the device took whole once, and is still taking after a seek.
type settling struct {
	fakeMechanism
	settles atomic.Int32
}

func (m *settling) Wait(ctx context.Context, _ <-chan struct{}) error {
	if m.settles.Add(1) == 1 {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestADeviceThatTookTheWholeStreamPlaysItOutBeforeTheCastEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newCoordinator(t.Context(), Cast{Device: observedDevice{wait: blocking}}, recovery.Attempt{})
		reachAttemptState(t, c.machine, playingBack)
		played := make(chan struct{})
		c.launch(deliverySettled, func() error { return nil })
		c.launch(playbackEnded, func() error {
			select {
			case <-played:
			case <-c.ctx.Done():
			}
			return nil
		})
		go func() {
			synctest.Wait()
			if !c.machine.Is(draining) {
				t.Errorf("the attempt is %s once the device took the whole stream, want draining", c.machine.State())
			}
			close(played)
		}()
		c.loop()
		if _, err := c.finish(); err != nil {
			t.Fatal(err)
		}
		if c.terminal == nil || c.terminal.event != playbackEnded {
			t.Errorf("the cast ended on %v, want the device saying it ended", c.terminal)
		}
	})
}

func TestADeviceThatNeverSaysItEndedIsLetGoOnceWhatItHeldHasPlayed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newCoordinator(t.Context(), Cast{Device: observedDevice{wait: blocking}}, recovery.Attempt{})
		reachAttemptState(t, c.machine, playingBack)
		c.launch(deliverySettled, func() error { return nil })
		c.launch(playbackEnded, func() error { <-c.ctx.Done(); return nil })
		began := time.Now()
		c.loop()
		if _, err := c.finish(); err != nil {
			t.Fatal(err)
		}
		if waited := time.Since(began); c.terminal == nil || c.terminal.event != drainElapsed || waited < drainMost {
			t.Errorf("the cast ended on %v after %v, want the drain to elapse after %v", c.terminal, waited, drainMost+drainGrace)
		}
	})
}

// heldDevice is a device its viewer holds until a time, at the start of what it took.
type heldDevice struct {
	observedDevice
	until time.Time
}

func (h heldDevice) Position(context.Context) (time.Duration, bool, bool) {
	return 0, time.Now().Before(h.until), true
}

func TestADeviceItsViewerHoldsIsNotLetGoWhileHeld(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := took(t, over)
		d := delivery{sink: p.mech, output: p.buf.reader}
		c := newCoordinator(t.Context(), Cast{Device: heldDevice{observedDevice: observedDevice{wait: blocking}, until: time.Now().Add(time.Hour)}}, recovery.Attempt{})
		reachAttemptState(t, c.machine, playingBack)
		c.presentation = presentation{delivery: &d, feed: feed{buffered: p.buf}}
		c.launch(deliverySettled, func() error { return nil })
		c.launch(playbackEnded, func() error { <-c.ctx.Done(); return nil })
		began := time.Now()
		c.loop()
		if _, err := c.finish(); err != nil {
			t.Fatal(err)
		}
		if waited := time.Since(began); c.terminal == nil || c.terminal.event != drainElapsed || waited < time.Hour {
			t.Errorf("the cast ended on %v after %v, want the drain to outlast the viewer's hour-long pause", c.terminal, waited)
		}
	})
}

func TestASeekWhileTheDevicePlaysOutBringsTheCastBackToPlaying(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := took(t, over)
		sought := make(chan struct{}, 1)
		mech := &settling{}
		d := delivery{sink: mech, output: p.buf.reader, sought: sought}
		c := newCoordinator(t.Context(), Cast{Device: p.dev}, recovery.Attempt{})
		reachAttemptState(t, c.machine, playingBack)
		c.presentation = presentation{delivery: &d, feed: feed{buffered: p.buf}}
		played := make(chan struct{})
		c.launch(playbackEnded, func() error {
			select {
			case <-played:
			case <-c.ctx.Done():
			}
			return nil
		})
		c.watchProduction()
		c.settle(&d)
		c.watchSeeks(&d)
		go func() {
			synctest.Wait()
			if !c.machine.Is(draining) {
				t.Errorf("the attempt is %s once the device took the whole stream, want draining", c.machine.State())
			}
			sought <- struct{}{}
			synctest.Wait()
			if !c.machine.Is(playingBack) {
				t.Errorf("the attempt is %s after the device asked for the stream from elsewhere, want playing", c.machine.State())
			}
			// Well past what the device held when it first took the whole stream.
			time.Sleep(drainMost + drainGrace + time.Minute)
			close(played)
		}()
		c.loop()
		if _, err := c.finish(); err != nil {
			t.Fatal(err)
		}
		if c.terminal == nil || c.terminal.event != playbackEnded {
			t.Errorf("the cast ended on %v, want the device saying it ended, not the drain the seek called off", c.terminal)
		}
	})
}

// resumedDevice is held until a time, then plays on from the start of what it took.
type resumedDevice struct {
	observedDevice
	until time.Time
}

func (r resumedDevice) Position(context.Context) (time.Duration, bool, bool) {
	if now := time.Now(); now.Before(r.until) {
		return 0, true, true
	}
	return time.Since(r.until), false, true
}

// madeUpTo is a delivery that made a set length of media.
type madeUpTo struct {
	producer
	length time.Duration
}

func (m madeUpTo) Progress() media.Progress { return media.Progress{Position: m.length} }

func TestADeviceResumedAfterAPauseIsLetGoOnlyOnceItPlayedWhatItHeld(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := took(t, over)
		held := 2 * time.Minute
		d := delivery{sink: p.mech, output: madeUpTo{producer: p.buf.reader, length: held}}
		// Resumed a second before the drain's third look, as a viewer would at any time.
		resumed := time.Now().Add(3*(held+drainGrace) - time.Second)
		c := newCoordinator(t.Context(), Cast{Device: resumedDevice{observedDevice: observedDevice{wait: blocking}, until: resumed}}, recovery.Attempt{})
		reachAttemptState(t, c.machine, playingBack)
		c.presentation = presentation{delivery: &d, feed: feed{buffered: p.buf}}
		c.launch(deliverySettled, func() error { return nil })
		c.launch(playbackEnded, func() error { <-c.ctx.Done(); return nil })
		c.loop()
		if _, err := c.finish(); err != nil {
			t.Fatal(err)
		}
		if c.terminal == nil || c.terminal.event != drainElapsed || time.Now().Before(resumed.Add(held)) {
			t.Errorf("the cast ended on %v at %v after the resume, want it to play the %v it held first", c.terminal, time.Since(resumed), held)
		}
	})
}

// castorHeld is a device castor paused while its read lagged, still paused until resumed.
type castorHeld struct {
	observedDevice
	resumed atomic.Bool
}

func (h *castorHeld) Resume(context.Context) error {
	h.resumed.Store(true)
	return nil
}

func (h *castorHeld) Position(context.Context) (time.Duration, bool, bool) {
	return 0, !h.resumed.Load(), true
}

func TestADeviceCastorHeldPlaysOutOnceTheWholeStreamWasTaken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := took(t, over)
		d := delivery{sink: p.mech, output: madeUpTo{producer: p.buf.reader, length: time.Minute}}
		dev := &castorHeld{wait: blocking}
		ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
		defer cancel()
		c := newCoordinator(ctx, Cast{Device: dev}, recovery.Attempt{})
		reachAttemptState(t, c.machine, playingBack)
		c.presentation = presentation{delivery: &d, feed: feed{buffered: p.buf}}
		c.launch(deliverySettled, func() error { return nil })
		c.launch(playbackEnded, func() error { <-c.ctx.Done(); return nil })
		c.loop()
		_, _ = c.finish()
		if !dev.resumed.Load() || c.terminal == nil || c.terminal.event != drainElapsed {
			t.Errorf("the cast ended on %v with the device resumed %v, want castor's hold lifted and the drain to elapse", c.terminal, dev.resumed.Load())
		}
	})
}
