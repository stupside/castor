package execute

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
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
		c := newCoordinator(t.Context(), Cast{}, recovery.Attempt{})
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
				t.Errorf("the attempt is %s once the device took the whole stream, want draining", c.machine.Current())
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
		c := newCoordinator(t.Context(), Cast{}, recovery.Attempt{})
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
				t.Errorf("the attempt is %s once the device took the whole stream, want draining", c.machine.Current())
			}
			sought <- struct{}{}
			synctest.Wait()
			if !c.machine.Is(playingBack) {
				t.Errorf("the attempt is %s after the device asked for the stream from elsewhere, want playing", c.machine.Current())
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
