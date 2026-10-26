package cast

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/telemetry"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"google.golang.org/protobuf/types/known/emptypb"
)

// play casts c's source on target and returns how it ended: the media server's word, unless this server ended it first.
func (s *Service) play(ctx context.Context, c *cast, target device.Info, asked *mediav1.PlaybackSettings) (result *castorv1.Ended) {
	ctx, finish := telemetry.Start(ctx, telemetry.Playback)
	defer func() {
		switch {
		case result.GetFailed() != nil:
			finish(errors.New("playback failed"))
		case result.GetStopped() != nil:
			finish(context.Canceled)
		default:
			finish(nil)
		}
	}()
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
		if failure := ended.GetFailed(); failure != nil {
			cause = errors.Join(cause, errors.New(failure.Message))
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
func (s *Service) run(ctx context.Context, c *cast, target device.Info, asked *mediav1.PlaybackSettings) (ended *castorv1.Ended, err error) {
	// Connected first, so an unreachable device costs no media cast and a slow one never outlasts the media server's wait for it.
	connectCtx, finishConnect := telemetry.Start(ctx, telemetry.Connect)
	lent, err := s.devices.Connect(connectCtx, target)
	finishConnect(err)
	if err != nil {
		code := castorv1.FailureCode_FAILURE_CODE_DEVICE_UNREACHABLE
		if _, busy := errors.AsType[*device.Busy](err); busy {
			code = castorv1.FailureCode_FAILURE_CODE_PLAYBACK_FAILED
		}
		return nil, &castFailure{code: code, err: fmt.Errorf("connecting to the device: %w", err)}
	}
	defer func() {
		if closeErr := lent.Close(); closeErr != nil {
			// Completion includes releasing the device. A failed final STOP/Close
			// must not be published as a successful or merely stopped cast.
			code := castorv1.FailureCode_FAILURE_CODE_PLAYBACK_FAILED
			if previous := ended.GetFailed(); previous != nil {
				code = previous.Code
				err = errors.Join(err, errors.New(previous.Message))
			} else if failure, ok := errors.AsType[*castFailure](err); ok {
				code = failure.code
			}
			ended = failedResult(code, errors.Join(err, fmt.Errorf("closing the device: %w", closeErr)))
			err = nil
		}
	}()
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

	return s.media.Run(ctx, c.id, source, asked, lent, c.mediaInfo, c.start, c.show)
}
