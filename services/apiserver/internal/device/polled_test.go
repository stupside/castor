package device

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// answers is a device polled for readings, the last one repeating, that gives up on the first poll past them if told to.
type answers struct {
	readings []Reading
	patience func() Patience
	polls    int
	refusal  error
}

func (a *answers) Read(context.Context) (Reading, error) {
	r := a.readings[min(a.polls, len(a.readings)-1)]
	a.polls++
	return r, nil
}

func (a *answers) Patience() Patience { return a.patience() }

func (a *answers) Refusal(Reading) error { return a.refusal }

func sayingIdle() Reading     { return Reading{Phase: Idle} }
func sayingHolding() Reading  { return Reading{Phase: Holding} }
func sayingSettling() Reading { return Reading{Phase: Settling} }
func sayingFault() Reading {
	return Reading{Phase: Idle, Fault: &PlaybackError{Err: errors.New("device error")}}
}
func repeated(r Reading, n int) []Reading { return slices.Repeat([]Reading{r}, n) }

func TestAPolledPlaybackEndsWhenTheDeviceGoesIdleAfterPlaying(t *testing.T) {
	for _, tt := range []struct {
		name      string
		readings  []Reading
		wantPolls int
	}{
		{"idle after playing", []Reading{sayingSettling(), sayingHolding(), sayingHolding(), sayingIdle()}, 4},
		{"idle while paused and back is not over until it is idle for good", []Reading{sayingHolding(), sayingHolding(), sayingIdle()}, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a := &answers{readings: tt.readings, patience: func() Patience { return IdlePolls(5) }}
				if err := AwaitPlayback(t.Context(), "test", "Living Room", "state", a); err != nil {
					t.Fatalf("AwaitPlayback = %v, want a clean end", err)
				}
				if a.polls != tt.wantPolls {
					t.Errorf("polled %d times, want %d", a.polls, tt.wantPolls)
				}
			})
		})
	}
}

func TestAPolledPlaybackIsRefusedWhenItsPatienceIsSpentBeforeItPlays(t *testing.T) {
	refused := errors.New("never played")
	for _, tt := range []struct {
		name      string
		readings  []Reading
		patience  func() Patience
		wantPolls int
		wantErr   error
	}{
		{"idle for as many polls as it is given", repeated(sayingIdle(), 5), func() Patience { return IdlePolls(3) }, 3, refused},
		{"idle for one poll fewer is not refused", slices.Concat(repeated(sayingIdle(), 2), []Reading{sayingHolding(), sayingIdle()}), func() Patience { return IdlePolls(3) }, 4, nil},
		{"idle streaks another state interrupts start again", slices.Concat(repeated(sayingIdle(), 2), []Reading{sayingSettling()}, repeated(sayingIdle(), 2), []Reading{sayingHolding(), sayingIdle()}), func() Patience { return IdlePolls(3) }, 7, nil},
		{"within the time it is given", repeated(sayingSettling(), 100), func() Patience { return Within(40 * time.Second) }, 0, refused},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a := &answers{readings: tt.readings, patience: tt.patience, refusal: refused}
				err := AwaitPlayback(t.Context(), "test", "Living Room", "state", a)
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("AwaitPlayback = %v, want %v", err, tt.wantErr)
				}
				if tt.wantPolls != 0 && a.polls != tt.wantPolls {
					t.Errorf("polled %d times, want %d", a.polls, tt.wantPolls)
				}
			})
		})
	}
}

func TestAFaultCountsOnlyWhereItEndsThePlayback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// A fault left from an earlier playback, read while the device is not playing yet, ends nothing.
		a := &answers{readings: []Reading{sayingFault(), sayingHolding(), sayingIdle()}, patience: func() Patience { return IdlePolls(5) }}
		if err := AwaitPlayback(t.Context(), "test", "Living Room", "state", a); err != nil || a.polls != 3 {
			t.Fatalf("AwaitPlayback = %v after %d polls, want a clean end after 3", err, a.polls)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		a := &answers{readings: []Reading{sayingHolding(), sayingFault()}, patience: func() Patience { return IdlePolls(5) }}
		_, failed := errors.AsType[*PlaybackError](AwaitPlayback(t.Context(), "test", "Living Room", "state", a))
		if !failed {
			t.Fatal("a fault read as the playback ended is not a playback error")
		}
	})
	synctest.Test(t, func(t *testing.T) {
		// A refusal that carries a fault is the fault, which says why.
		a := &answers{readings: repeated(sayingFault(), 3), patience: func() Patience { return IdlePolls(2) }, refusal: errors.New("never played")}
		_, failed := errors.AsType[*PlaybackError](AwaitPlayback(t.Context(), "test", "Living Room", "state", a))
		if !failed {
			t.Fatal("a refusal read with a fault is not the playback error")
		}
	})
}
