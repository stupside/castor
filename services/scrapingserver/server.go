// Package scrapingserver exposes page extraction as a standalone RPC boundary.
// It owns the browser and returns candidates, never ranking or playing them.
package scrapingserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	scrapingv1 "github.com/stupside/castor/gen/castor/scraping/v1"
	"github.com/stupside/castor/gen/castor/scraping/v1/scrapingv1connect"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/admission"
	"github.com/stupside/castor/internal/transport"
)

// Resolver is implemented by the composition root that owns the browser.
type Resolver interface {
	Resolve(context.Context, []string) ([]*castorv1.StreamCandidate, error)
}
type Server struct {
	http.Handler
	resolver Resolver
	requests *admission.Gate
	shut     context.CancelCauseFunc
}

const maxRequests = 16

var errShutdown = errors.New("the scraping server is shutting down")

func New(resolver Resolver) *Server {
	life, shut := context.WithCancelCause(context.Background())
	s := &Server{requests: admission.New(life, maxRequests), resolver: resolver, shut: shut}
	mux := http.NewServeMux()
	mux.Handle(scrapingv1connect.NewScrapingServiceHandler(s, transport.Options()))
	transport.Introspect(mux, scrapingv1connect.ScrapingServiceName)
	s.Handler = mux
	return s
}

func (s *Server) Resolve(ctx context.Context, req *scrapingv1.ResolveRequest) (*scrapingv1.ResolveResponse, error) {
	working, leave, err := s.requests.Enter(ctx)
	switch {
	case errors.Is(err, admission.ErrFull):
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("concurrent request capacity exhausted"))
	case err != nil:
		return nil, connect.NewError(connect.CodeUnavailable, errShutdown)
	}
	defer leave()
	streams, err := s.resolver.Resolve(working, req.GetUrls())
	if err != nil {
		code := connect.CodeNotFound
		switch {
		case errors.Is(err, context.Canceled):
			code = connect.CodeCanceled
		case errors.Is(err, context.DeadlineExceeded):
			code = connect.CodeDeadlineExceeded
		}
		return nil, s.requests.Answer(working, connect.NewError(code, err))
	}
	if len(streams) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no streams extracted"))
	}
	return &scrapingv1.ResolveResponse{Streams: streams}, nil
}

func (s *Server) Shutdown(ctx context.Context) {
	s.shut(errShutdown)
	_ = s.requests.Wait(ctx)
}

var _ scrapingv1connect.ScrapingServiceHandler = (*Server)(nil)
