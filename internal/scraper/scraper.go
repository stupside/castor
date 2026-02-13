package scraper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/stupside/castor/internal/app"
	"github.com/stupside/castor/internal/media"
)

// Scraper is a data-driven video source configured from YAML.
type Scraper struct {
	name      string
	proxies   []string
	templates app.TemplateConfig
	capture   app.CaptureConfig
}

// NewScraper creates a Scraper from a ScraperConfig.
func NewScraper(cfg app.ScraperConfig) *Scraper {
	return &Scraper{
		name:      cfg.Name,
		proxies:   cfg.Proxies,
		templates: cfg.Templates,
		capture:   cfg.Capture,
	}
}

// Name returns the scraper's name.
func (s *Scraper) Name() string {
	return s.name
}

// Movie extracts a movie stream URL using the configured template.
func (s *Scraper) Movie(ctx context.Context, browserCfg app.BrowserConfig, itemID string) (*media.Stream, error) {
	path := strings.ReplaceAll(s.templates.Movie, "{itemID}", itemID)
	return s.extract(ctx, browserCfg, path)
}

// Episode extracts an episode stream URL using the configured template.
func (s *Scraper) Episode(ctx context.Context, browserCfg app.BrowserConfig, itemID string, season, episode uint) (*media.Stream, error) {
	path := s.templates.Episode
	path = strings.ReplaceAll(path, "{itemID}", itemID)
	path = strings.ReplaceAll(path, "{season}", fmt.Sprintf("%d", season))
	path = strings.ReplaceAll(path, "{episode}", fmt.Sprintf("%d", episode))
	return s.extract(ctx, browserCfg, path)
}

func (s *Scraper) extract(ctx context.Context, browserCfg app.BrowserConfig, path string) (*media.Stream, error) {
	var errs []error

	for _, proxy := range s.proxies {
		if ctx.Err() != nil {
			break
		}

		targetURL := fmt.Sprintf("%s%s", proxy, path)

		slog.InfoContext(ctx, "Attempting to capture stream URL", "proxy", proxy, "path", path, "targetURL", targetURL)

		rawURL, err := captureStreamURL(ctx, browserCfg, targetURL, s.capture)
		if err != nil {
			errs = append(errs, fmt.Errorf("proxy %s: %w", proxy, err))
			continue
		}

		slog.InfoContext(ctx, "Captured stream URL", "url", rawURL)

		u, err := url.Parse(rawURL)
		if err != nil {
			errs = append(errs, fmt.Errorf("parsing captured URL %q: %w", rawURL, err))
			continue
		}

		ct := media.DetectFromExtension(u)
		if ct == "" {
			ct = media.HLS
		}

		return &media.Stream{URL: u, ContentType: ct}, nil
	}

	return nil, fmt.Errorf("all proxies failed for scraper %q: %w", s.name, errors.Join(errs...))
}
