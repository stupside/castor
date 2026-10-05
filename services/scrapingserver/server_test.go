package scrapingserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	scrapingv1 "github.com/stupside/castor/gen/castor/scraping/v1"
	"github.com/stupside/castor/gen/castor/scraping/v1/scrapingv1connect"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/transport"
)

type resolveFunc func(context.Context, []string) ([]*castorv1.StreamCandidate, error)

func (f resolveFunc) Resolve(ctx context.Context, urls []string) ([]*castorv1.StreamCandidate, error) {
	return f(ctx, urls)
}

func TestScrapingTokenAndHealthAreIndependent(t *testing.T) {
	s := New(resolveFunc(func(context.Context, []string) ([]*castorv1.StreamCandidate, error) {
		return []*castorv1.StreamCandidate{{Stream: &castorv1.Stream{Url: "https://cdn.example/title.mp4"}}}, nil
	}))
	ts := httptest.NewServer(transport.Authorized(s, "scraping-secret"))
	t.Cleanup(ts.Close)
	t.Cleanup(func() { s.Shutdown(t.Context()) })
	for _, tc := range []struct {
		token string
		code  connect.Code
	}{{"", connect.CodeUnauthenticated}, {"media-secret", connect.CodeUnauthenticated}, {"scraping-secret", 0}} {
		_, err := scrapingv1connect.NewScrapingServiceClient(transport.Bearer(tc.token), ts.URL).Resolve(t.Context(), &scrapingv1.ResolveRequest{Urls: []string{"https://site.example/watch"}})
		if tc.code == 0 {
			if err != nil {
				t.Errorf("authenticated extraction failed: %v", err)
			}
			continue
		}
		if connect.CodeOf(err) != tc.code {
			t.Errorf("token %q: %v, want %v", tc.token, err, tc.code)
		}
	}
	health, err := grpchealth.NewClient(http.DefaultClient, ts.URL).Check(t.Context(), &grpchealth.CheckRequest{})
	if err != nil || health.Status != grpchealth.StatusServing {
		t.Fatalf("health requires authentication: %v %v", health, err)
	}
}

func TestScrapingReportsEmptyExtraction(t *testing.T) {
	s := New(resolveFunc(func(context.Context, []string) ([]*castorv1.StreamCandidate, error) { return nil, nil }))
	ts := httptest.NewTestServer(t, s)
	t.Cleanup(func() { s.Shutdown(t.Context()) })
	c := scrapingv1connect.NewScrapingServiceClient(ts.Client(), ts.URL)
	if _, err := c.Resolve(t.Context(), &scrapingv1.ResolveRequest{Urls: []string{"https://site.example/watch"}}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("empty extraction: %v, want not found", err)
	}
}

func TestScrapingShutdownCancelsAndWaitsForExtraction(t *testing.T) {
	looking, cleaned := make(chan struct{}), make(chan struct{})
	s := New(resolveFunc(func(ctx context.Context, _ []string) ([]*castorv1.StreamCandidate, error) {
		close(looking)
		<-ctx.Done()
		close(cleaned)
		return nil, ctx.Err()
	}))
	ts := httptest.NewTestServer(t, s)
	c := scrapingv1connect.NewScrapingServiceClient(ts.Client(), ts.URL)
	done := make(chan error, 1)
	go func() {
		_, err := c.Resolve(t.Context(), &scrapingv1.ResolveRequest{Urls: []string{"https://site.example/watch"}})
		done <- err
	}()
	select {
	case <-looking:
	case <-time.After(5 * time.Second):
		t.Fatal("resolver did not start")
	}
	s.Shutdown(t.Context())
	select {
	case <-cleaned:
	default:
		t.Fatal("shutdown did not wait for cleanup")
	}
	if err := <-done; !errors.Is(err, context.Canceled) && connect.CodeOf(err) != connect.CodeCanceled {
		t.Errorf("shutdown returned %v, want canceled", err)
	}
	if _, err := c.Resolve(t.Context(), &scrapingv1.ResolveRequest{Urls: []string{"https://site.example/watch"}}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("request after shutdown: %v", err)
	}
}

func TestScrapingPreservesResolverCancellationCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code connect.Code
	}{{context.Canceled, connect.CodeCanceled}, {context.DeadlineExceeded, connect.CodeDeadlineExceeded}} {
		s := New(resolveFunc(func(context.Context, []string) ([]*castorv1.StreamCandidate, error) {
			return nil, fmt.Errorf("browser: %w", tc.err)
		}))
		_, err := s.Resolve(t.Context(), &scrapingv1.ResolveRequest{Urls: []string{"https://site.example/watch"}})
		s.Shutdown(t.Context())
		if connect.CodeOf(err) != tc.code {
			t.Errorf("resolver %v became %v", tc.err, err)
		}
	}
}
