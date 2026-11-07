package cast

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

// playback shares the cast's existing leased device; it never opens a second connection.
type playback struct {
	device.Device
	lifetime context.Context
	loaded   atomic.Pointer[loadedPlayback]
	control  sync.Mutex
	watch    sync.Mutex
	changed  chan struct{}
	requests chan *playbackSeek
	reload   bool
}

type loadedPlayback struct {
	duration, base time.Duration
	media          device.Playback
}

func (p *playback) Play(ctx context.Context, media device.Playback) error {
	return p.playAt(ctx, media, 0)
}

func (p *playback) playAt(ctx context.Context, media device.Playback, base time.Duration) error {
	if err := p.Device.Play(ctx, media); err != nil {
		return err
	}
	p.loaded.Store(&loadedPlayback{duration: base + media.Duration(), base: base, media: media})
	p.rewatch()
	return nil
}

func (p *playback) state(ctx context.Context) (*castorv1.PlaybackState, error) {
	state := &castorv1.PlaybackState{}
	loaded := p.loaded.Load()
	if loaded == nil {
		return state, nil
	}
	if loaded.duration > 0 {
		state.Duration = durationpb.New(loaded.duration)
	}
	head, err := p.Device.Position(ctx)
	if errors.Is(err, device.ErrNoPosition) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	state.Position = durationpb.New(loaded.base + max(0, head.At))
	state.Paused = head.Paused
	controls := device.ControlsOf(p.Device)
	state.CanPause = controls.Pause
	state.CanSeek = (controls.Seek || p.canReload(loaded)) && loaded.duration > 0
	return state, nil
}

func (s *Service) activePlayback(id string) (*playback, error) {
	c, err := s.find(id)
	if err != nil {
		return nil, err
	}
	view, _ := c.now.Load()
	p := c.playback.Load()
	if view.ended != nil || p != nil && p.lifetime.Err() != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("this cast has ended"))
	}
	return p, nil
}

// playbackContext keeps an in-flight command inside both the request and its cast.
func playbackContext(ctx context.Context, p *playback) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(p.lifetime, func() { cancel(context.Cause(p.lifetime)) })
	if cause := context.Cause(p.lifetime); cause != nil {
		cancel(cause)
	}
	return ctx, func() { stop(); cancel(nil) }
}

func (s *Service) Playback(ctx context.Context, req *castorv1.PlaybackRequest) (*castorv1.PlaybackResponse, error) {
	p, err := s.activePlayback(req.GetCastId())
	if err != nil {
		return nil, err
	}
	if p == nil {
		return &castorv1.PlaybackResponse{Playback: &castorv1.PlaybackState{}}, nil
	}
	ctx, cancel := playbackContext(ctx, p)
	defer cancel()
	state, err := p.state(ctx)
	if err != nil {
		return nil, err
	}
	return &castorv1.PlaybackResponse{Playback: state}, nil
}

func (s *Service) Control(ctx context.Context, req *castorv1.ControlRequest) (*castorv1.ControlResponse, error) {
	p, err := s.activePlayback(req.GetCastId())
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the receiver is not playing yet"))
	}
	ctx, cancel := playbackContext(ctx, p)
	defer cancel()
	p.control.Lock()
	defer p.control.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state, err := p.state(ctx)
	if err != nil {
		return nil, err
	}
	if state.Position == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the receiver has not reported its playback state"))
	}
	switch action := req.GetAction().(type) {
	case *castorv1.ControlRequest_Pause:
		if !state.CanPause {
			return nil, connect.NewError(connect.CodeUnimplemented, device.ErrUnsupportedControl)
		}
		err = p.Device.Pause(ctx)
	case *castorv1.ControlRequest_Resume:
		if !state.CanPause {
			return nil, connect.NewError(connect.CodeUnimplemented, device.ErrUnsupportedControl)
		}
		err = p.Device.Resume(ctx)
	case *castorv1.ControlRequest_Seek:
		at := action.Seek.AsDuration()
		if !state.CanSeek {
			return nil, connect.NewError(connect.CodeUnimplemented, device.ErrUnsupportedControl)
		}
		if at < 0 || at > state.Duration.AsDuration() {
			return nil, connect.NewError(connect.CodeOutOfRange, errors.New("the position is outside this video"))
		}
		err = p.seek(ctx, at)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a playback control action is required"))
	}
	if err != nil {
		return nil, err
	}
	state, err = p.state(ctx)
	if err != nil {
		return nil, err
	}
	return &castorv1.ControlResponse{Playback: state}, nil
}
