package cast

import (
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/stupside/castor/internal/machine"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/lend"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// castEvent keeps this machine's events distinct at Go call sites.
type castEvent uint8

const (
	eventLendStream castEvent = iota
	eventMeasure
	eventRank
	eventAttempt
	eventRevise
	eventEnd
	eventAbandon
)

func (e castEvent) String() string {
	return [...]string{
		eventLendStream: "lend-stream",
		eventMeasure:    "measure",
		eventRank:       "rank",
		eventAttempt:    "attempt",
		eventRevise:     "revise",
		eventEnd:        "end",
		eventAbandon:    "abandon",
	}[e]
}

type castState uint8

const (
	stateAwaiting castState = iota
	stateMeasuring
	stateCasting
	stateEnded
)

func (s castState) String() string {
	return [...]string{
		stateAwaiting:  "awaiting",
		stateMeasuring: "measuring",
		stateCasting:   "casting",
		stateEnded:     "ended",
	}[s]
}

// lifecycle validates cast progress. The owner publishes the terminal snapshot
// after attempt cleanup, then signals completion to the device lender.
func lifecycle() *machine.Machine[castState, castEvent] {
	return machine.New("cast", stateAwaiting, []castState{stateEnded}, []machine.Edge[castState, castEvent]{
		{On: eventLendStream, From: []castState{stateAwaiting}, To: stateMeasuring},
		{On: eventMeasure, From: []castState{stateMeasuring}, To: stateMeasuring},
		{On: eventRank, From: []castState{stateMeasuring}, To: stateMeasuring},
		{On: eventAttempt, From: []castState{stateMeasuring, stateCasting}, To: stateCasting},
		{On: eventRevise, From: []castState{stateCasting}, To: stateCasting},
		{On: eventEnd, From: []castState{stateMeasuring, stateCasting}, To: stateEnded},
		// Only a cast still awaiting is abandoned, so a grace that runs out as a device is lent changes nothing.
		{On: eventAbandon, From: []castState{stateAwaiting}, To: stateEnded},
	}, nil)
}

// fire takes event if the cast's state allows it, then publishes what change makes of its snapshot; it reports whether it took it.
func (c *cast) fire(event castEvent, change func(*view)) bool {
	taken := false
	ended := false
	c.now.Update(func(now view) (view, bool) {
		next := view{status: proto.CloneOf(now.status), ended: now.ended}
		if err := c.machine.Fire(c.ctx, event); err != nil {
			// Duplicate lending, expired admission timers and updates after
			// completion are expected races. Other rejections are owner bugs.
			if !errors.Is(err, machine.ErrRefused) || !(c.machine.Is(stateEnded) || event == eventLendStream || event == eventAbandon) {
				slog.ErrorContext(c.ctx, "cast transition failed", "event", event, "error", err)
			}
			return now, false
		}
		change(&next)
		taken = true
		ended = c.machine.Is(stateEnded)
		return next, true
	})
	// Publish the terminal snapshot before waking registry and device owners.
	// Only the caller that took the terminal transition signals completion.
	if ended {
		close(c.done)
		c.cancel(nil)
	}
	return taken
}

// lend gives the cast its device, as its lender connected it, and starts the cast; a cast takes one device, and none once it is over.
func (c *cast) lend(caps media.Capabilities) error {
	if !c.fire(eventLendStream, func(*view) {}) {
		if c.machine.Is(stateEnded) {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("this cast is over"))
		}
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("this cast already has its device"))
	}
	go c.run(caps)
	return nil
}

// end ends the cast by event with outcome: nil ended, errStopped stopped, else why it failed.
func (c *cast) end(event castEvent, outcome error) {
	ended := &castorv1.Ended{Result: &castorv1.Ended_Completed{Completed: &emptypb.Empty{}}}
	interrupted, _ := errors.AsType[*recovery.Interrupted](outcome)
	switch {
	case errors.Is(outcome, errStopped) && (interrupted == nil || interrupted.Failure == nil):
		ended.Result = &castorv1.Ended_Stopped{Stopped: &emptypb.Empty{}}
	case outcome != nil:
		ended.Result = &castorv1.Ended_Failed{Failed: &castorv1.Failure{Code: failureCode(outcome), Message: outcome.Error()}}
	}
	c.fire(event, func(next *view) { next.ended = ended })
}

// failureCode names failures from their causes, preserving the error's account for people.
func failureCode(err error) castorv1.FailureCode {
	switch {
	case errors.Is(err, errShutdown):
		return castorv1.FailureCode_FAILURE_CODE_SERVER_SHUTDOWN
	case errors.Is(err, errUndriven), errors.Is(err, lend.ErrLenderLeft):
		return castorv1.FailureCode_FAILURE_CODE_DEVICE_UNREACHABLE
	}
	if _, gone := errors.AsType[*media.Gone](err); gone {
		return castorv1.FailureCode_FAILURE_CODE_DEVICE_UNREACHABLE
	}
	return castorv1.FailureCode_FAILURE_CODE_PLAYBACK_FAILED
}
