package cast

import (
	"context"
	"errors"
	"fmt"
	"time"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/awake"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/lend"
)

// run finds the source's streams, readies them and casts them on d, then ends with how that went.
func (c *cast) run(d *lend.Device) {
	defer awake.Hold()()
	go d.Follow(c.ctx)
	c.end(eventEnd, outcome(c.ctx, c.play(d)))
}

func (c *cast) play(d *lend.Device) error {
	streams, err := c.source.streams()
	if err != nil {
		return err
	}
	c.fire(eventMeasure, func(next *view) { next.status.GetMeasuring().Streams = uint32(len(streams)) })
	ready, err := c.source.ready(c.ctx, c.caster, streams)
	if err != nil {
		return err
	}
	c.fire(eventRank, func(next *view) { next.status.GetMeasuring().Castable = new(uint32(len(ready))) })
	return c.caster.Play(c.ctx, d, c.publisher, ready, c.start, c, c.seeks)
}

// seek moves the cast to at in its media, refused while no attempt plays it.
func (c *cast) seek(at time.Duration) error {
	accepted := make(chan error, 1)
	select {
	case c.seeks <- recovery.Seek{At: at, Accepted: accepted}:
	default:
		if c.ctx.Err() != nil {
			return errEnded
		}
		return lend.ErrNotPlaying
	}
	if err := <-accepted; err != nil {
		return err
	}
	if d := c.device.Load(); d != nil {
		d.Reload()
	}
	return nil
}

// outcome preserves failures discovered while joining a canceled cast.
func outcome(ctx context.Context, err error) error {
	cause := context.Cause(ctx)
	if cause == nil {
		return err
	}
	if errors.Is(cause, context.Canceled) {
		cause = errStopped
	}
	if err == nil || err == context.Canceled {
		return cause
	}
	if errors.Is(err, cause) {
		return err
	}
	return errors.Join(cause, err)
}

// Attempting shows the cast on its try-th attempt.
func (c *cast) Attempting(try int) {
	c.fire(eventAttempt, func(next *view) {
		if next.status.GetCasting() == nil {
			measured := next.status.GetMeasuring()
			next.status.State = &castorv1.CastStatus_Casting{Casting: &castorv1.CastingStatus{
				Streams: measured.GetStreams(), Castable: measured.GetCastable(),
			}}
		}
		next.status.GetCasting().Attempt = uint32(try)
	})
}

// Revising shows the action the cast is revising its attempt with, and why.
func (c *cast) Revising(action recovery.Action, why string) {
	c.fire(eventRevise, func(next *view) {
		next.status.GetCasting().Revision = &castorv1.Revision{Action: recoveryAction(action), Why: why}
	})
}

// recoveryAction translates the engine's actions at the contract edge.
func recoveryAction(action recovery.Action) castorv1.RecoveryAction {
	switch action {
	case recovery.SwitchCandidate:
		return castorv1.RecoveryAction_RECOVERY_ACTION_SWITCH_CANDIDATE
	case recovery.DecodeAxis:
		return castorv1.RecoveryAction_RECOVERY_ACTION_DECODE_AXIS
	case recovery.RelaxRead:
		return castorv1.RecoveryAction_RECOVERY_ACTION_RELAX_READ
	case recovery.ServeInstead:
		return castorv1.RecoveryAction_RECOVERY_ACTION_SERVE_INSTEAD
	case recovery.Stay:
		return castorv1.RecoveryAction_RECOVERY_ACTION_STAY
	default:
		panic(fmt.Sprintf("unknown recovery action %q", action))
	}
}
