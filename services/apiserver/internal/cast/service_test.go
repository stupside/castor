package cast

import (
	"testing"

	"connectrpc.com/connect"
	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

func TestPagesWithoutScrapingFailWithoutCallingMedia(t *testing.T) {
	s := New(&mediav1.PlaybackSettings{}, nil, nil, nil)
	defer s.Drain(t.Context())
	_, err := s.Resolve(t.Context(), &castorv1.ResolveRequest{Source: &castorv1.Source{Source: &castorv1.Source_Pages_{Pages: &castorv1.Source_Pages{Urls: []string{"https://site.example/watch"}}}}})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("missing scraping resolver: %v, want unavailable (no media fallback)", err)
	}
}
