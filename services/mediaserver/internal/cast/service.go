// Package cast runs each media cast behind the media contract: its lifecycle, the device lent to it, and what devices fetch from it.
package cast

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"time"

	"connectrpc.com/connect"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/internal/registry"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
	"github.com/stupside/castor/services/mediaserver/internal/cast/execute"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// linger keeps an ended cast findable, so a watcher that arrives late still learns how it ended.
const linger = 5 * time.Minute

var errShutdown = errors.New("server shutting down")

// Caster ranks resolved candidates or measures a direct stream, then plays it.
type Caster interface {
	Rank(ctx context.Context, streams []*source.Stream) ([]*source.Stream, error)
	Measure(ctx context.Context, stream *source.Stream) (*source.Stream, error)
	Play(ctx context.Context, device execute.Device, listeners deliver.Listeners, streams []*source.Stream, turns recovery.Turns) error
}

// Service runs every cast behind the media contract: it starts, stops and follows them, takes the device each is lent, and ranks streams without casting.
type Service struct {
	ctx    context.Context
	shut   context.CancelCauseFunc
	caster func(asked *mediav1.PlaybackSettings) Caster
	reach  *url.URL
	casts  *registry.Registry[*cast]
}

// New casts with caster, telling devices to reach the media route at reach.
func New(caster func(asked *mediav1.PlaybackSettings) Caster, reach *url.URL) *Service {
	// Casts outlive the request that started them, and end only once the server shuts down.
	ctx, shut := context.WithCancelCause(context.Background())
	return &Service{ctx: ctx, shut: shut, caster: caster, reach: reach, casts: registry.New[*cast](linger)}
}

func (s *Service) Start(_ context.Context, req *mediav1.StartRequest) (*mediav1.StartResponse, error) {
	c := newCast(s.ctx, rand.Text(), s.reach, s.caster(req.GetSettings()), req.GetSource())
	if !s.casts.Add(c.id, c, c.done) {
		c.cancel(errShutdown)
		return nil, connect.NewError(connect.CodeUnavailable, errShutdown)
	}
	return &mediav1.StartResponse{CastId: c.id}, nil
}

func (s *Service) Stop(_ context.Context, req *mediav1.StopRequest) (*mediav1.StopResponse, error) {
	c, err := s.find(req.GetCastId())
	if err != nil {
		return nil, err
	}
	c.cancel(errStopped)
	return &mediav1.StopResponse{}, nil
}

// Serves reports whether cast id serves port to devices.
func (s *Service) Serves(id, port string) bool {
	c, ok := s.casts.Find(id)
	return ok && c.deliveries.Serves(port)
}

// Drain ends every cast, failed as the server shuts down, and waits until they have let go of what they hold, or ctx ends.
func (s *Service) Drain(ctx context.Context) {
	s.shut(errShutdown)
	s.casts.Drain(ctx)
}

func (s *Service) find(id string) (*cast, error) {
	c, ok := s.casts.Find(id)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no cast %q", id))
	}
	return c, nil
}
