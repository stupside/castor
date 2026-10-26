package mediaclient

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/looplab/fsm"
	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/telemetry"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

// sessionEvent keeps this machine's events distinct at Go call sites.
type sessionEvent uint8

const (
	remoteStarted sessionEvent = iota
	remoteEnded
	sessionCancelled
	sessionFailed
	stopSettled
	sessionJoined
)

func (e sessionEvent) String() string {
	return [...]string{
		remoteStarted:    "remote-started",
		remoteEnded:      "remote-ended",
		sessionCancelled: "cancelled",
		sessionFailed:    "failed",
		stopSettled:      "stop-settled",
		sessionJoined:    "joined",
	}[e]
}

const (
	controlTimeout = 5 * time.Second
	stopBackoff    = 100 * time.Millisecond
	// stopGrace bounds reconciliation and the wait for remote completion together.
	stopGrace = 5 * time.Second
)

const (
	sessionStarting = "starting"
	sessionActive   = "active"
	sessionStopping = "stopping"
	sessionJoining  = "joining"
	sessionFinished = "finished"
)

// As in the attempt coordinator, the owner consumes events and the library
// validates transitions. Network operations and joins run outside FSM callbacks.
func newSessionMachine() *fsm.FSM {
	track := telemetry.Track("session", sessionStarting, sessionFinished)
	return fsm.NewFSM(sessionStarting, fsm.Events{
		{Name: remoteStarted.String(), Src: []string{sessionStarting}, Dst: sessionActive},
		{Name: remoteEnded.String(), Src: []string{sessionActive}, Dst: sessionJoining},
		{Name: sessionCancelled.String(), Src: []string{sessionStarting, sessionActive}, Dst: sessionStopping},
		{Name: sessionFailed.String(), Src: []string{sessionStarting, sessionActive}, Dst: sessionStopping},
		{Name: stopSettled.String(), Src: []string{sessionStopping}, Dst: sessionJoining},
		{Name: sessionJoined.String(), Src: []string{sessionJoining}, Dst: sessionFinished},
	}, fsm.Callbacks{"after_event": func(ctx context.Context, e *fsm.Event) { track(ctx, e.Event, e.Src, e.Dst) }})
}

type watchResult struct {
	ended *castorv1.Ended
	err   error
}

// session owns the remote protocol and borrows the device through worker joins.
// Only its event loop changes state; workers report results through channels.
type session struct {
	client   *Client
	id       string
	ctx      context.Context
	cancel   context.CancelFunc
	machine  *fsm.FSM
	workers  sync.WaitGroup
	watching <-chan watchResult
	driving  <-chan error
	result   watchResult
	failure  error
}

// Run owns one remote cast's control protocol, including reconciliation when
// Start's response is lost. It returns only after releasing every device borrower.
func (c *Client) Run(parent context.Context, id string, source *mediav1.Source, asked *mediav1.PlaybackSettings, lent device.Device, info *castorv1.MediaInfo, start time.Duration, status func(*castorv1.CastStatus)) (*castorv1.Ended, error) {
	if cause := context.Cause(parent); cause != nil {
		return nil, cause
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	s := &session{client: c, id: id, ctx: ctx, cancel: cancel, machine: newSessionMachine()}
	// Start has a bounded lifetime independent of caller cancellation: once
	// sent, its ID must be reconciled even if its response never arrives.
	starting, stop := context.WithTimeout(ctx, controlTimeout)
	s.failure = c.Start(starting, id, source, asked, start)
	stop()
	if s.failure != nil {
		s.move(sessionFailed)
	} else {
		s.move(remoteStarted)
		s.observe(parent, lent, info, status)
		s.loop(parent)
	}
	return s.finish()
}

func (s *session) move(event sessionEvent) {
	// Cancellation is an input, never a reason to abort bookkeeping.
	if err := s.machine.Event(context.WithoutCancel(s.ctx), event.String()); err != nil {
		s.failure = errors.Join(s.failure, err)
		if s.machine.Can(sessionFailed.String()) {
			s.failure = errors.Join(s.failure, s.machine.Event(context.WithoutCancel(s.ctx), sessionFailed.String()))
		}
	}
}

func (s *session) observe(parent context.Context, lent device.Device, info *castorv1.MediaInfo, status func(*castorv1.CastStatus)) {
	watched := make(chan watchResult, 1)
	s.watching = watched
	s.workers.Go(func() {
		ended, err := s.client.Watch(s.ctx, s.id, status)
		watched <- watchResult{ended, err}
	})
	if parent.Err() == nil {
		driven := make(chan error, 1)
		s.driving = driven
		s.workers.Go(func() {
			err := s.client.Drive(s.ctx, s.id, lent, info)
			if s.ctx.Err() != nil {
				err = nil
			}
			driven <- err
		})
	}
}

func (s *session) loop(parent context.Context) {
	for s.machine.Is(sessionActive) {
		if parent.Err() != nil {
			s.move(sessionCancelled)
			break
		}
		select {
		case <-parent.Done():
			s.move(sessionCancelled)
		case err := <-s.driving:
			s.driving = nil
			if err != nil {
				s.failure = fmt.Errorf("driving the device: %w", err)
				s.move(sessionFailed)
			}
		case s.result = <-s.watching:
			s.watching = nil
			if s.result.ended == nil {
				s.move(sessionFailed)
			} else {
				s.move(remoteEnded)
			}
		}
	}
}

func (s *session) finish() (*castorv1.Ended, error) {
	var stopErr error
	if s.machine.Is(sessionStopping) {
		stopping, stop := context.WithTimeout(s.ctx, stopGrace)
		stopErr = s.client.stopRemote(stopping, s.id)
		if s.watching != nil {
			select {
			case s.result = <-s.watching:
				s.watching = nil
			case <-stopping.Done():
				s.result.err = context.Cause(stopping)
			}
		}
		stop()
		s.move(stopSettled)
	}
	s.cancel()
	s.workers.Wait()
	if s.watching != nil {
		// Completion may have raced the grace deadline. Joining makes the
		// final watch result available even when the select chose the timeout.
		if result := <-s.watching; result.ended != nil {
			s.result = result
		}
	}
	if s.driving != nil {
		if err := <-s.driving; err != nil {
			s.failure = errors.Join(s.failure, fmt.Errorf("driving the device: %w", err))
		}
	}
	// A terminal remote result confirms completion even if Stop's reply
	// was lost. Without it, retain reconciliation failures.
	if s.result.ended == nil {
		s.failure = errors.Join(s.failure, stopErr)
	}
	s.move(sessionJoined)
	if s.failure != nil {
		return nil, errors.Join(s.failure, s.result.err)
	}
	return s.result.ended, s.result.err
}

// stopRemote retries lost stop requests within the owner's reconciliation budget.
func (c *Client) stopRemote(ctx context.Context, id string) error {
	for backoff := stopBackoff; ; backoff = min(2*backoff, controlTimeout) {
		err := c.Stop(ctx, id)
		if err == nil || connect.CodeOf(err) == connect.CodeNotFound {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, context.Cause(ctx))
		case <-time.After(backoff):
		}
	}
}
