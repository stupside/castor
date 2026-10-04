package cast

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/looplab/fsm"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/lend"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// A cast waits for its device, measures its streams, plays them, and ends once.
const (
	stateAwaiting  = "awaiting"
	stateMeasuring = "measuring"
	stateCasting   = "casting"
	stateEnded     = "ended"

	// Lending a device starts measuring, for both a direct stream and candidates.
	eventLendStream = "lend-stream"
	eventMeasure    = "measure"
	eventRank       = "rank"
	eventAttempt    = "attempt"
	eventRevise     = "revise"
	eventEnd        = "end"
	eventAbandon    = "abandon"
)

// lifecycle is the moves a cast may make; reaching its end releases the device and everything the cast holds.
func (c *cast) lifecycle() *fsm.FSM {
	return fsm.NewFSM(stateAwaiting, fsm.Events{
		{Name: eventLendStream, Src: []string{stateAwaiting}, Dst: stateMeasuring},
		{Name: eventMeasure, Src: []string{stateMeasuring}, Dst: stateMeasuring},
		{Name: eventRank, Src: []string{stateMeasuring}, Dst: stateMeasuring},
		{Name: eventAttempt, Src: []string{stateMeasuring, stateCasting}, Dst: stateCasting},
		{Name: eventRevise, Src: []string{stateCasting}, Dst: stateCasting},
		{Name: eventEnd, Src: []string{stateMeasuring, stateCasting}, Dst: stateEnded},
		// Only a cast still awaiting is abandoned, so a grace that runs out as a device is lent changes nothing.
		{Name: eventAbandon, Src: []string{stateAwaiting}, Dst: stateEnded},
	}, fsm.Callbacks{
		"enter_" + stateEnded: func(context.Context, *fsm.Event) {
			close(c.done)
			c.cancel(nil)
		},
	})
}

// fire takes event if the cast's state allows it, then publishes what change makes of its snapshot; it reports whether it took it.
func (c *cast) fire(event string, change func(*view)) bool {
	taken := false
	c.now.Update(func(now view) (view, bool) {
		// Never the cast's context: a cancelled one would abort the transition that ends it.
		err := c.machine.Event(context.Background(), event)
		if _, stayed := errors.AsType[fsm.NoTransitionError](err); err != nil && !stayed {
			return now, false
		}
		next := view{status: proto.CloneOf(now.status), ended: now.ended}
		if c.machine.Is(stateCasting) && next.status.GetCasting() == nil {
			measured := next.status.GetMeasuring()
			next.status.State = &castorv1.CastStatus_Casting{Casting: &castorv1.CastingStatus{
				Streams: measured.GetStreams(), Castable: measured.GetCastable(),
			}}
		}
		change(&next)
		taken = true
		return next, true
	})
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
func (c *cast) end(event string, outcome error) {
	ended := &castorv1.Ended{Result: &castorv1.Ended_Completed{Completed: &emptypb.Empty{}}}
	switch {
	case errors.Is(outcome, errStopped):
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
