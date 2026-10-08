// Package extract captures video stream URLs from a page with headless Chrome.
package extract

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
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

func (e *Extractor) extract(ctx context.Context, targetURL string) ([]*castorv1.StreamCandidate, error) {
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

// Resolve captures unranked candidates from pages within the browser concurrency budget.
func (e *Extractor) Resolve(ctx context.Context, urls []string) ([]*castorv1.StreamCandidate, error) {
	slog.InfoContext(ctx, "extracting streams", "urls", len(urls))

	results := make([][]*castorv1.StreamCandidate, len(urls))
	failures := make([]error, len(urls))
	// extract holds each page to the browser budget itself.
	var wg sync.WaitGroup
	for i, targetURL := range urls {
		if ctx.Err() != nil {
			break
		}
		wg.Go(func() {
			slog.DebugContext(ctx, "extracting", "url", targetURL, "index", i+1, "total", len(urls))
			streams, err := e.extract(ctx, targetURL)
			if err != nil {
				slog.WarnContext(ctx, "extraction failed", "url", targetURL, "error", err)
				failures[i] = fmt.Errorf("%s: %w", targetURL, err)
				return
			}
			results[i] = streams
			slog.DebugContext(ctx, "extracted", "url", targetURL, "count", len(streams))
		})
	}
	wg.Wait()
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

func deduplicate(streams []*castorv1.StreamCandidate) []*castorv1.StreamCandidate {
	seen := make(map[string]struct{}, len(streams))
	return slices.DeleteFunc(slices.Clone(streams), func(s *castorv1.StreamCandidate) bool {
		key := s.GetStream().GetUrl()
		if _, ok := seen[key]; ok {
			return true
		}
		seen[key] = struct{}{}
		return false
	})
}
