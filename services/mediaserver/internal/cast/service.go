// Package cast runs each media cast behind the media contract: its lifecycle, the device lent to it, and what devices fetch from it.
package cast

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"

	"connectrpc.com/connect"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/internal/registry"
	"github.com/stupside/castor/internal/telemetry"
	"github.com/stupside/castor/services/mediaserver/internal/cast/execute"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"golang.org/x/sync/semaphore"
)

// linger keeps an ended cast findable, so a watcher that arrives late still learns how it ended.
const linger = 5 * time.Minute

const resolutionCapacity = 16

var errShutdown = errors.New("server shutting down")

// Caster ranks resolved candidates or measures a direct stream, then plays it.
type Caster interface {
	Rank(ctx context.Context, streams []*source.Stream) ([]*source.Stream, error)
	Measure(ctx context.Context, stream *source.Stream) (*source.Stream, error)
	Play(ctx context.Context, device execute.Device, publisher output.Publisher, streams []*source.Stream, start time.Duration, turns recovery.Turns) error
}

// Service runs every cast behind the media contract: it starts, stops and follows them, takes the device each is lent, and ranks streams without casting.
type Service struct {
	watchers    *semaphore.Weighted
	resolutions *semaphore.Weighted
	ctx         context.Context
	shut        context.CancelCauseFunc
	caster      func(asked *mediav1.PlaybackSettings) Caster
	publisher   output.Publisher
	casts       *registry.Registry[*cast]
}

// New runs casts with caster and publishes their media through publisher.
func New(caster func(asked *mediav1.PlaybackSettings) Caster, publisher output.Publisher) *Service {
	// Casts outlive the request that started them, and end only once the server shuts down.
	ctx, shut := context.WithCancelCause(context.Background())
	return &Service{watchers: semaphore.NewWeighted(256), resolutions: semaphore.NewWeighted(resolutionCapacity), ctx: ctx, shut: shut, caster: caster, publisher: publisher, casts: registry.New[*cast](linger)}
}

func (s *Service) Start(ctx context.Context, req *mediav1.StartRequest) (*mediav1.StartResponse, error) {
	c := newCast(telemetry.WithTrace(s.ctx, ctx), req.GetCastId(), s.publisher, s.caster(req.GetSettings()), req.GetSource(), req.GetStart().AsDuration())
	if err := s.casts.Add(c.id, c, c.done); err != nil {
		c.cancel(errShutdown)
		if errors.Is(err, registry.ErrCapacity) {
			return nil, connect.NewError(connect.CodeResourceExhausted, err)
		}
		if errors.Is(err, registry.ErrTaken) {
			return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("cast %q", c.id))
		}
		return nil, connect.NewError(connect.CodeUnavailable, errShutdown)
	}
	return &mediav1.StartResponse{}, nil
}

func (s *Service) Stop(_ context.Context, req *mediav1.StopRequest) (*mediav1.StopResponse, error) {
	c, err := s.find(req.GetCastId())
	if err != nil {
		return nil, err
	}
	c.cancel(errStopped)
	return &mediav1.StopResponse{}, nil
}

// Drain cancels casts and source resolutions, then waits for their cleanup or ctx to end.
func (s *Service) Drain(ctx context.Context) {
	s.shut(errShutdown)
	s.casts.Drain(ctx)
	// Resolution requests own probes and source readers too; shutdown must join them.
	if s.resolutions.Acquire(ctx, resolutionCapacity) == nil {
		s.resolutions.Release(resolutionCapacity)
	}
}

func (s *Service) find(id string) (*cast, error) {
	c, ok := s.casts.Find(id)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no cast %q", id))
	}
	return c, nil
}
