package mediaclient

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"google.golang.org/protobuf/types/known/emptypb"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/gen/castor/media/v1/mediav1connect"
)

type unavailableCast struct {
	mediav1connect.CastServiceClient
	err   error
	calls int
}

func (c *unavailableCast) Stop(context.Context, *mediav1.StopRequest) (*mediav1.StopResponse, error) {
	c.calls++
	return nil, c.err
}

func TestStopReconciliationHasOneBoundedBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lost := errors.New("stop response lost")
		remote := &unavailableCast{err: lost}
		client := &Client{casts: remote}
		ctx, cancel := context.WithTimeout(t.Context(), stopGrace)
		defer cancel()
		start := time.Now()
		err := client.stopRemote(ctx, "cast")
		if !errors.Is(err, lost) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("lost reconciliation cause: %v", err)
		}
		if remote.calls < 2 || time.Since(start) != stopGrace {
			t.Fatalf("calls=%d elapsed=%v", remote.calls, time.Since(start))
		}
	})
}

func TestRemoteCompletionDuringJoinSurvivesStopTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		watched := make(chan watchResult, 1)
		ended := &castorv1.Ended{Result: &castorv1.Ended_Stopped{Stopped: &emptypb.Empty{}}}
		s := &session{
			client: &Client{casts: &unavailableCast{err: errors.New("stop response lost")}},
			id:     "cast", ctx: ctx, cancel: cancel, machine: newSessionMachine(), watching: watched,
		}
		s.move(remoteStarted)
		s.move(sessionCancelled)
		s.workers.Go(func() {
			<-ctx.Done()
			// Watch decoded completion just as the owner ended its wait.
			watched <- watchResult{ended: ended}
		})
		got, err := s.finish()
		if err != nil || got != ended || !s.machine.Is(sessionFinished) {
			t.Fatalf("lost remote completion during join: ended=%v err=%v state=%s", got, err, s.machine.State())
		}
	})
}

func TestCancelledSessionDoesNotStartRemoteWork(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("caller stopped before start")
	cancel(cause)
	// No ports are installed: an already cancelled call must not use them.
	ended, err := (&Client{}).Run(ctx, "cast", nil, nil, nil, nil, 0, nil)
	if ended != nil || !errors.Is(err, cause) {
		t.Fatalf("ended=%v err=%v", ended, err)
	}
}
