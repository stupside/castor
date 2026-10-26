package execute

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
)

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
