package cast

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

const (
	// controlTimeout bounds a start or stop on the media server, which run past the caller's cancellation.
	controlTimeout = 5 * time.Second
	// stopBackoff is the first wait before a stop the media server did not take is sent again.
	stopBackoff = 100 * time.Millisecond
	// stopGrace is how long a stopped cast waits to hear the media server's end before it lets go of the device anyway.
	stopGrace = 5 * time.Second
)

// play casts c's source on target and returns how it ended: the media server's word, unless this server ended it first.
func (s *Service) play(ctx context.Context, c *cast, target device.Info, asked *mediav1.PlaybackSettings) *castorv1.Ended {
	ended, err := s.run(ctx, c, target, asked)
	cause := context.Cause(ctx)
	switch {
	case cause != nil && !errors.Is(cause, errStopped) && !errors.Is(cause, context.Canceled):
		code := castorv1.FailureCode_FAILURE_CODE_INTERNAL
		if errors.Is(cause, errShutdown) {
			code = castorv1.FailureCode_FAILURE_CODE_SERVER_SHUTDOWN
		}
		if failure, ok := errors.AsType[*castFailure](cause); ok {
			code = failure.code
		}
		return failedResult(code, cause)
	case ended != nil:
		return ended
	case cause != nil:
		return &castorv1.Ended{Result: &castorv1.Ended_Stopped{Stopped: &emptypb.Empty{}}}
	}
	if failure, ok := errors.AsType[*castFailure](err); ok {
		return failedResult(failure.code, err)
	}
	return failedResult(castorv1.FailureCode_FAILURE_CODE_INTERNAL, fromMedia(err))
}

type castFailure struct {
	code castorv1.FailureCode
	err  error
}

func (e *castFailure) Error() string { return e.err.Error() }
func (e *castFailure) Unwrap() error { return e.err }

func failedResult(code castorv1.FailureCode, err error) *castorv1.Ended {
	return &castorv1.Ended{Result: &castorv1.Ended_Failed{Failed: &castorv1.Failure{Code: code, Message: err.Error()}}}
}

// run connects target, runs c on the media server and lends it the device until the media server's cast has ended.
func (s *Service) run(ctx context.Context, c *cast, target device.Info, asked *mediav1.PlaybackSettings) (*castorv1.Ended, error) {
	// Connected first, so an unreachable device costs no media cast and a slow one never outlasts the media server's wait for it.
	lent, err := s.devices.Connect(ctx, target)
	if err != nil {
		return nil, &castFailure{code: castorv1.FailureCode_FAILURE_CODE_DEVICE_UNREACHABLE, err: fmt.Errorf("connecting to the device: %w", err)}
	}
	defer func() { _ = lent.Close() }()
	if c.source.GetPages() != nil {
		c.show(&castorv1.CastStatus{State: &castorv1.CastStatus_Extracting{Extracting: &emptypb.Empty{}}})
	}
	source, err := s.resolve(ctx, c.source)
	if err != nil {
		code := castorv1.FailureCode_FAILURE_CODE_EXTRACTION_FAILED
		if connect.CodeOf(err) == connect.CodeInternal {
			code = castorv1.FailureCode_FAILURE_CODE_INTERNAL
		}
		return nil, &castFailure{code: code, err: fmt.Errorf("resolving source: %w", err)}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Started past the caller's cancellation, so no media cast is left that nothing knows to stop.
	starting, cancel := context.WithTimeout(context.WithoutCancel(ctx), controlTimeout)
	media, err := s.media.Start(starting, source, asked)
	cancel()
	if err != nil {
		return nil, err
	}
	c.update(func(v *view) { v.media = media })

	watching, unwatch := context.WithCancel(context.WithoutCancel(ctx))
	defer unwatch()
	stopMedia := s.stopper(ctx, media, watching)
	defer context.AfterFunc(ctx, stopMedia)()
	defer context.AfterFunc(ctx, func() { time.AfterFunc(stopGrace, unwatch) })()

	// The device is driven until the media server's cast ends, through its teardown too.
	driving, leave := context.WithCancel(context.WithoutCancel(ctx))
	var wg sync.WaitGroup
	if ctx.Err() == nil {
		wg.Go(func() {
			if err := s.media.Drive(driving, media, lent, c.device); err != nil && driving.Err() == nil {
				c.stop(&castFailure{code: castorv1.FailureCode_FAILURE_CODE_INTERNAL, err: fmt.Errorf("driving the device: %w", err)})
			}
		})
	}
	ended, err := s.media.Watch(watching, media, nil, c.show, nil)
	unwatch()
	leave()
	wg.Wait()
	if ended == nil {
		// The watch broke, so the media server's cast may still run.
		stopMedia()
	}
	return ended, err
}

// stopper stops media cast id once, sending the stop again while the cast's watch is open, since one lost on the way leaves it playing.
func (s *Service) stopper(ctx context.Context, id string, watching context.Context) func() {
	return sync.OnceFunc(func() {
		for backoff := stopBackoff; ; backoff = min(2*backoff, controlTimeout) {
			stopping, cancel := context.WithTimeout(context.WithoutCancel(ctx), controlTimeout)
			err := s.media.Stop(stopping, id)
			cancel()
			if err == nil || connect.CodeOf(err) == connect.CodeNotFound {
				return
			}
			select {
			case <-watching.Done():
				return
			case <-time.After(backoff):
			}
		}
	})
}
