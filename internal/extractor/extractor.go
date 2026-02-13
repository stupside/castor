package extractor

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"

	"golang.org/x/sync/errgroup"

	"github.com/stupside/castor/internal/app"
	"github.com/stupside/castor/internal/media"
)

// Extractor captures video stream URLs from a page using headless Chrome.
// It holds only capture and action config (patterns, timing) — no proxies or templates.
type Extractor struct {
	browser  app.BrowserConfig
	capture  app.CaptureConfig
	actions  app.ActionConfig
	patterns []*regexp.Regexp
}

// NewExtractor creates an Extractor from a BrowserConfig, CaptureConfig, and ActionConfig.
func NewExtractor(browserCfg app.BrowserConfig, cfg app.CaptureConfig, actionCfg app.ActionConfig) (*Extractor, error) {
	e := &Extractor{
		browser: browserCfg,
		capture: cfg,
		actions: actionCfg,
	}

	for i, p := range cfg.Patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("pattern #%d: %w", i, err)
		}
		e.patterns = append(e.patterns, re)
	}

	return e, nil
}

// Extract runs a single session+pipeline extraction attempt.
func (e *Extractor) Extract(ctx context.Context, targetURL string) ([]*media.Stream, error) {
	session, err := newSession(ctx, e, targetURL)
	if err != nil {
		return nil, fmt.Errorf("creating session for %s: %w", targetURL, err)
	}
	defer session.Close()

	session.RunActions(e.actions)

	entries, err := session.collector.Wait(ctx, e.capture.GraceAfterActions, e.capture.CollectionWindow)
	if err != nil {
		return nil, fmt.Errorf("waiting for streams on %s: %w", targetURL, err)
	}

	var streams []*media.Stream
	for _, entry := range entries {
		u, err := url.Parse(entry.RawURL)
		if err != nil {
			slog.DebugContext(ctx, "skipping entry: invalid URL", "raw_url", entry.RawURL, "error", err)
		} else {
			ct := media.DetectFromExtension(u)
			if ct == "" {
				ct = media.DetectFromMIME(entry.MimeType)
			}
			if ct == "" {
				slog.DebugContext(ctx, "skipping entry: unknown type", "url", u.String())
				continue
			}
			streams = append(streams, &media.Stream{URL: u, Headers: entry.Headers, ContentType: ct})
		}
	}

	if len(streams) == 0 {
		return nil, fmt.Errorf("no usable streams found (%d entries captured, none with recognized content type)", len(entries))
	}

	return streams, nil
}

// ExtractAll runs Extract concurrently on all given URLs (bounded by the
// extractor's MaxConcurrency) and returns deduplicated streams.
func ExtractAll(ctx context.Context, e *Extractor, urls []string) ([]*media.Stream, error) {
	var g errgroup.Group
	g.SetLimit(e.capture.MaxConcurrency)

	results := make([][]*media.Stream, len(urls))

	for i, targetURL := range urls {
		g.Go(func() error {
			streams, err := e.Extract(ctx, targetURL)
			if err != nil {
				return fmt.Errorf("%s: %w", targetURL, err)
			}
			results[i] = streams
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		slog.Warn("some URLs failed extraction", "error", err)
	}

	var allStreams []*media.Stream
	for _, ss := range results {
		allStreams = append(allStreams, ss...)
	}

	return deduplicateStreams(allStreams), nil
}

// deduplicateStreams removes duplicate streams by URL string.
func deduplicateStreams(streams []*media.Stream) []*media.Stream {
	out := make([]*media.Stream, 0, len(streams))
	seen := make(map[string]struct{}, len(streams))
	for _, s := range streams {
		key := s.URL.String()
		if _, ok := seen[key]; ok {
			continue
		}
		out = append(out, s)
		seen[key] = struct{}{}
	}
	return out
}
