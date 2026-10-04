package apiserver

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stupside/castor/internal/transport"
	"github.com/urfave/cli/v3"
)

func TestEmbeddedCompositionStartsOnlyMissingServices(t *testing.T) {
	for _, tc := range []struct {
		name            string
		media, scraping bool
	}{
		{"both local", true, true}, {"remote media", false, true},
		{"remote scraping", true, false}, {"both remote", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaults()
			if !tc.media {
				cfg.Server.URL = "http://media.example:8410"
			}
			if !tc.scraping {
				cfg.Scraping.URL = "http://scraping.example:8412"
			}
			var mediaRuns, scrapingRuns, mediaStops, scrapingStops int
			media := func(context.Context, *cli.Command, slog.Handler) (transport.Endpoint, func(), error) {
				mediaRuns++
				return transport.Endpoint{URL: "http://localhost:8410", Token: "media-only"}, func() { mediaStops++ }, nil
			}
			scraping := func(context.Context, *cli.Command) (transport.Endpoint, func(), error) {
				scrapingRuns++
				return transport.Endpoint{URL: "http://localhost:8412", Token: "scraping-only"}, func() { scrapingStops++ }, nil
			}
			srv, stop, err := cfg.server(t.Context(), &cli.Command{}, media, scraping, slog.Default().Handler())
			if err != nil {
				t.Fatal(err)
			}
			srv.Shutdown(t.Context())
			stop()
			wantMedia, wantScraping := 0, 0
			if tc.media {
				wantMedia = 1
			}
			if tc.scraping {
				wantScraping = 1
			}
			if mediaRuns != wantMedia || mediaStops != wantMedia || scrapingRuns != wantScraping || scrapingStops != wantScraping {
				t.Fatalf("started/stopped media %d/%d, scraping %d/%d; want %d, %d", mediaRuns, mediaStops, scrapingRuns, scrapingStops, wantMedia, wantScraping)
			}
		})
	}
}

func TestEmbeddedResolverFailureStopsAlreadyStartedMedia(t *testing.T) {
	cfg := defaults()
	stopped := false
	media := func(context.Context, *cli.Command, slog.Handler) (transport.Endpoint, func(), error) {
		return transport.Endpoint{URL: "http://localhost:8410"}, func() { stopped = true }, nil
	}
	failure := errors.New("resolver unavailable")
	scraping := func(context.Context, *cli.Command) (transport.Endpoint, func(), error) {
		return transport.Endpoint{}, nil, failure
	}
	if _, _, err := cfg.server(t.Context(), &cli.Command{}, media, scraping, slog.Default().Handler()); !errors.Is(err, failure) || !stopped {
		t.Fatalf("failed composition leaked media: %v, stopped=%v", err, stopped)
	}
}
