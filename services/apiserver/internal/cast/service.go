// Package cast runs each cast end to end: the device it lends, the media server's cast, and how it ended.
package cast

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/registry"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/mediaclient"
)

// linger keeps an ended cast findable, so a watcher that arrives late still learns how it ended.
const linger = 5 * time.Minute

var (
	errStopped  = errors.New("cast stopped")
	errShutdown = errors.New("server shutting down")
)

// Devices is where a cast finds the device it targets and connects it.
type Devices interface {
	Target(ctx context.Context, t *castorv1.Target) (device.Info, error)
	Connect(ctx context.Context, target device.Info) (device.Device, error)
}

type Resolver interface {
	ResolvePages(context.Context, []string) ([]*castorv1.StreamCandidate, error)
}

// Service runs every cast on the device it targets, each played through the media server.
type Service struct {
	ctx      context.Context
	shut     context.CancelCauseFunc
	media    *mediaclient.Client
	scraping Resolver
	defaults *mediav1.PlaybackSettings
	devices  Devices
	casts    *registry.Registry[*cast]
}

// New casts on devices through media, every cast asking defaults unless its request says otherwise.
func New(defaults *mediav1.PlaybackSettings, devices Devices, media *mediaclient.Client, scraping Resolver) *Service {
	// Casts outlive the request that started them, and end only once the server shuts down.
	running, shut := context.WithCancelCause(context.Background())
	return &Service{ctx: running, shut: shut, media: media, scraping: scraping, defaults: proto.CloneOf(defaults), devices: devices, casts: registry.New[*cast](linger)}
}

func (s *Service) Cast(ctx context.Context, req *castorv1.CastRequest) (*castorv1.CastResponse, error) {
	if s.ctx.Err() != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errShutdown)
	}
	finding, cancel := context.WithCancel(ctx)
	stopFinding := context.AfterFunc(s.ctx, cancel)
	defer stopFinding()
	defer cancel()
	target, err := s.devices.Target(finding, req.GetTarget())
	if s.ctx.Err() != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errShutdown)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	asked := s.asked(req.GetPreferences())
	run, stop := context.WithCancelCause(s.ctx)
	c := newCast(rand.Text(), target.Public(), proto.CloneOf(req.GetSource()), stop)
	done := make(chan struct{})
	if !s.casts.Add(c.id, c, done) {
		stop(nil)
		return nil, connect.NewError(connect.CodeUnavailable, errShutdown)
	}
	go func() {
		defer close(done)
		defer stop(nil)
		ended := s.play(run, c, target, asked)
		c.update(func(v *view) { v.ended = ended })
		slog.Info("cast ended", "id", c.id, "device", cmp.Or(c.device.GetName(), c.device.GetAddress()), "completed", ended.GetCompleted() != nil, "stopped", ended.GetStopped() != nil, "failure", ended.GetFailed())
	}()
	return &castorv1.CastResponse{CastId: c.id}, nil
}

// Drain takes no new cast, ends every running one as the server shuts down, and waits for them to end, or for ctx.
func (s *Service) Drain(ctx context.Context) {
	s.shut(errShutdown)
	s.casts.Drain(ctx)
}

// asked is what a cast asks: the server's defaults, overridden field by field by the request's.
func (s *Service) asked(p *castorv1.Preferences) *mediav1.PlaybackSettings {
	asked := proto.CloneOf(s.defaults)
	if p != nil {
		if p.Delivery != nil {
			asked.Delivery = p.GetDelivery()
		}
		if p.MaxHeight != nil {
			asked.MaxHeight = p.GetMaxHeight()
		}
		if p.Subtitles != nil {
			asked.Subtitles = proto.CloneOf(p.Subtitles)
		}
	}
	return asked
}

func (s *Service) Resolve(ctx context.Context, req *castorv1.ResolveRequest) (*castorv1.ResolveResponse, error) {
	source, err := s.resolve(ctx, req.GetSource())
	if err != nil {
		return nil, err
	}
	ranked, err := s.media.Rank(ctx, source, s.asked(req.GetPreferences()))
	if err != nil {
		return nil, fromMedia(err)
	}
	return &castorv1.ResolveResponse{Ranked: ranked}, nil
}

// resolve is the only translation from public sources to media candidates.
func (s *Service) resolve(ctx context.Context, source *castorv1.Source) (*mediav1.Source, error) {
	if one := source.GetStream(); one != nil {
		return &mediav1.Source{Source: &mediav1.Source_Stream{Stream: one}}, nil
	}
	if ready := source.GetStreams(); ready != nil {
		return &mediav1.Source{Source: &mediav1.Source_Streams_{Streams: &mediav1.Source_Streams{Streams: ready.GetStreams()}}}, nil
	}
	if s.scraping == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("page resolution requires scrapingserver"))
	}
	streams, err := s.scraping.ResolvePages(ctx, source.GetPages().GetUrls())
	if err != nil {
		switch connect.CodeOf(err) {
		case connect.CodeUnauthenticated, connect.CodePermissionDenied, connect.CodeInvalidArgument:
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scrapingserver refused the API server (check scraping.token): %w", err))
		}
		return nil, err
	}
	if len(streams) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no stream candidates found"))
	}
	return &mediav1.Source{Source: &mediav1.Source_Streams_{Streams: &mediav1.Source_Streams{Streams: streams}}}, nil
}

// fromMedia is err from the media server as this API answers it: the media server refusing this server is this server's fault, not the caller's.
func fromMedia(err error) error {
	switch connect.CodeOf(err) {
	case connect.CodeUnauthenticated, connect.CodePermissionDenied, connect.CodeInvalidArgument:
		return connect.NewError(connect.CodeInternal, fmt.Errorf("the media server refused this API server (check server.token): %w", err))
	}
	return err
}

func (s *Service) Stop(_ context.Context, req *castorv1.StopRequest) (*castorv1.StopResponse, error) {
	c, err := s.find(req.GetCastId())
	if err != nil {
		return nil, err
	}
	c.stop(errStopped)
	return &castorv1.StopResponse{}, nil
}

func (s *Service) ListCasts(context.Context, *castorv1.ListCastsRequest) (*castorv1.ListCastsResponse, error) {
	var playing []*cast
	for c := range s.casts.All() {
		if v, _ := c.now.Load(); v.ended == nil {
			playing = append(playing, c)
		}
	}
	slices.SortFunc(playing, func(a, b *cast) int { return cmp.Or(a.started.Compare(b.started), cmp.Compare(a.id, b.id)) })
	listed := make([]*castorv1.Cast, len(playing))
	for i, c := range playing {
		v, _ := c.now.Load()
		listed[i] = &castorv1.Cast{Id: c.id, Device: proto.CloneOf(c.device), Source: shareable(c.source), Status: proto.CloneOf(v.status)}
	}
	return &castorv1.ListCastsResponse{Casts: listed}, nil
}

// shareable is source without the headers captured where its stream was found, which may hold a viewer's session.
func shareable(source *castorv1.Source) *castorv1.Source {
	out := proto.CloneOf(source)
	if stream := out.GetStream(); stream != nil {
		stream.Headers = nil
	}
	if streams := out.GetStreams(); streams != nil {
		for _, candidate := range streams.GetStreams() {
			candidate.GetStream().Headers = nil
		}
	}
	return out
}

func (s *Service) find(id string) (*cast, error) {
	c, ok := s.casts.Find(id)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no cast %q", id))
	}
	return c, nil
}
