// Package extract captures video stream URLs from a page with headless Chrome.
package extract

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/stupside/castor/services/scrapingserver/internal/streaminfo"
)

// Extractor opens pages in a browser and captures the streams they fetch.
type Extractor struct {
	cfg   Config
	slots chan struct{}
}

func New(cfg Config) *Extractor {
	cfg.Capture.MaxConcurrency = max(1, cfg.Capture.MaxConcurrency)
	return &Extractor{cfg: cfg, slots: make(chan struct{}, cfg.Capture.MaxConcurrency)}
}

func (e *Extractor) extract(ctx context.Context, targetURL string) ([]*streaminfo.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !webPage(targetURL) {
		return nil, fmt.Errorf("target %q is not a web page", targetURL)
	}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, pageBudget)
	defer cancel()

	session, err := newSession(ctx, e.cfg.Browser, targetURL)
	if err != nil {
		return nil, fmt.Errorf("creating session for %s: %w", targetURL, err)
	}
	defer session.Close()

	session.runActions()

	streams, err := session.collector.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("waiting for streams on %s: %w", targetURL, err)
	}
	for _, stream := range streams {
		stream.SourcePage = targetURL
	}
	return streams, nil
}

// ExtractAll extracts every url at once, within the parallelism the config allows.
func (e *Extractor) ExtractAll(ctx context.Context, urls []string) ([]*streaminfo.Stream, error) {
	slog.InfoContext(ctx, "extracting streams", "urls", len(urls))

	results := make([][]*streaminfo.Stream, len(urls))
	failures := make([]error, len(urls))
	var g errgroup.Group
	g.SetLimit(e.cfg.Capture.MaxConcurrency)
	for i, targetURL := range urls {
		if ctx.Err() != nil {
			break
		}
		g.Go(func() error {
			slog.DebugContext(ctx, "extracting", "url", targetURL, "index", i+1, "total", len(urls))
			streams, err := e.extract(ctx, targetURL)
			if err != nil {
				slog.WarnContext(ctx, "extraction failed", "url", targetURL, "error", err)
				failures[i] = fmt.Errorf("%s: %w", targetURL, err)
				return nil
			}
			results[i] = streams
			slog.DebugContext(ctx, "extracted", "url", targetURL, "count", len(streams))
			return nil
		})
	}
	_ = g.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	deduped := deduplicate(slices.Concat(results...))
	if len(deduped) == 0 {
		if err := errors.Join(failures...); err != nil {
			return nil, fmt.Errorf("no stream extracted from %d URL(s): %w", len(urls), err)
		}
		return nil, fmt.Errorf("no stream extracted from %d URL(s)", len(urls))
	}
	slog.InfoContext(ctx, "extraction complete", "urls", len(urls), "streams", len(deduped))
	return deduped, nil
}

func deduplicate(streams []*streaminfo.Stream) []*streaminfo.Stream {
	seen := make(map[string]struct{}, len(streams))
	return slices.DeleteFunc(slices.Clone(streams), func(s *streaminfo.Stream) bool {
		key := s.URL.String()
		if _, ok := seen[key]; ok {
			return true
		}
		seen[key] = struct{}{}
		return false
	})
}
