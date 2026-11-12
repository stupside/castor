package admission

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"connectrpc.com/connect"
)

func TestAGateLetsInUpToItsCapacityWhileItsLifeLasts(t *testing.T) {
	shutdown := errors.New("shutting down")
	life, end := context.WithCancelCause(t.Context())
	g := New(life, 1)
	release, err := g.Take()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Take(); !errors.Is(err, ErrFull) {
		t.Errorf("a second work at capacity 1: %v, want ErrFull", err)
	}
	release()
	end(shutdown)
	if _, err := g.Take(); !errors.Is(err, shutdown) {
		t.Errorf("a work once life ended: %v, want the cause it ended with", err)
	}
}

func TestAnEndedLifeEndsTheWorkLetInAndWaitJoinsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		shutdown := errors.New("shutting down")
		life, end := context.WithCancelCause(t.Context())
		g := New(life, 2)
		working, leave, err := g.Enter(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		joined := make(chan struct{})
		go func() { _ = g.Wait(t.Context()); close(joined) }()
		end(shutdown)
		<-working.Done()
		if cause := context.Cause(working); !errors.Is(cause, shutdown) {
			t.Errorf("work ended with %v, want the life's cause", cause)
		}
		synctest.Wait()
		select {
		case <-joined:
			t.Fatal("Wait returned before the work let in left")
		default:
		}
		leave()
		<-joined
	})
}

func TestFailedWorkAnswersWhyItEnded(t *testing.T) {
	shutdown := errors.New("shutting down")
	own := errors.New("its own failure")
	life, end := context.WithCancelCause(t.Context())
	g := New(life, 3)
	running, leaveRunning, _ := g.Enter(t.Context())
	defer leaveRunning()
	caller, giveUp := context.WithCancel(t.Context())
	abandoned, leaveAbandoned, _ := g.Enter(caller)
	defer leaveAbandoned()
	cut, leaveCut, _ := g.Enter(t.Context())
	defer leaveCut()
	if got := g.Answer(running, own); got != own {
		t.Errorf("work that failed on its own answered %v, want its error", got)
	}
	giveUp()
	end(shutdown)
	<-cut.Done()
	if got := g.Answer(abandoned, own); !errors.Is(got, context.Canceled) || connect.CodeOf(got) == connect.CodeUnavailable {
		t.Errorf("work whose caller gave up before the shutdown answered %v, want the caller's end", got)
	}
	if got := g.Answer(cut, own); connect.CodeOf(got) != connect.CodeUnavailable || !errors.Is(got, shutdown) {
		t.Errorf("work the shutdown cut off answered %v, want the shutdown", got)
	}
}
