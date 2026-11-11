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
	"golang.org/x/sync/semaphore"
)

// Resolver is implemented by the composition root that owns the browser.
type Resolver interface {
	Resolve(context.Context, []string) ([]*castorv1.StreamCandidate, error)
}
type Server struct {
	http.Handler
	resolver Resolver
	requests *semaphore.Weighted
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
}

const maxRequests = 16

var errShutdown = errors.New("the scraping server is shutting down")

func New(resolver Resolver) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{requests: semaphore.NewWeighted(maxRequests), resolver: resolver, ctx: ctx, cancel: cancel}
	mux := http.NewServeMux()
	mux.Handle(scrapingv1connect.NewScrapingServiceHandler(s, transport.Options()))
	transport.Introspect(mux, scrapingv1connect.ScrapingServiceName)
	s.Handler = mux
	return s
}

func (s *Server) Resolve(ctx context.Context, req *scrapingv1.ResolveRequest) (*scrapingv1.ResolveResponse, error) {
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, connect.NewError(connect.CodeUnavailable, errShutdown)
	}
	if !s.requests.TryAcquire(1) {
		s.mu.Unlock()
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("concurrent request capacity exhausted"))
	}
	defer s.requests.Release(1)
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	streams, err := s.resolver.Resolve(ctx, req.GetUrls())
	if s.ctx.Err() != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errShutdown)
	}
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
	// Admission has stopped. Taking every slot joins the accepted requests;
	// the context bounds the wait without leaving a waiter goroutine behind.
	if s.requests.Acquire(ctx, maxRequests) == nil {
		s.requests.Release(maxRequests)
	}
}

var _ scrapingv1connect.ScrapingServiceHandler = (*Server)(nil)
