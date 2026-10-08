// Package scrapingserver exposes page extraction as a standalone RPC boundary.
// It owns the browser and returns candidates, never ranking or playing them.
package scrapingserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"connectrpc.com/connect"
	scrapingv1 "github.com/stupside/castor/gen/castor/scraping/v1"
	"github.com/stupside/castor/gen/castor/scraping/v1/scrapingv1connect"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/transport"
)

// Resolver is implemented by the composition root that owns the browser.
type Resolver interface {
	Resolve(context.Context, []string) ([]*castorv1.StreamCandidate, error)
}
type Server struct {
	http.Handler
	resolver Resolver
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	wg       sync.WaitGroup
}

func New(resolver Resolver) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{resolver: resolver, ctx: ctx, cancel: cancel}
	mux := http.NewServeMux()
	mux.Handle(scrapingv1connect.NewScrapingServiceHandler(s, transport.Checked()))
	transport.Introspect(mux, scrapingv1connect.ScrapingServiceName)
	s.Handler = mux
	return s
}

func (s *Server) Resolve(ctx context.Context, req *scrapingv1.ResolveRequest) (*scrapingv1.ResolveResponse, error) {
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, connect.NewError(connect.CodeUnavailable, context.Canceled)
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	streams, err := s.resolver.Resolve(ctx, req.GetUrls())
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, connect.NewError(connect.CodeCanceled, err)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, connect.NewError(connect.CodeDeadlineExceeded, err)
		}
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if len(streams) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no streams extracted"))
	}
	return &scrapingv1.ResolveResponse{Streams: streams}, nil
}

func (s *Server) Shutdown(ctx context.Context) {
	s.mu.Lock()
	s.cancel()
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

var _ scrapingv1connect.ScrapingServiceHandler = (*Server)(nil)
