// Castor is a proof of concept provided for lawful, personal, and educational
// use. This file is part of its stream-extraction pipeline and is intended only
// for accessing content you are authorized to view. Do not use it to infringe
// copyright or to circumvent access controls. The author does not endorse or
// condone piracy. See the "Purpose and disclaimer" section of the README.

package extract

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"sync"

	"github.com/stupside/castor/internal/media"
)

// Extractor captures video stream URLs from a page using headless Chrome.
// It holds only capture and action config (patterns, timing) — no proxies or templates.
type Extractor struct {
	browser  BrowserConfig
	capture  CaptureConfig
	actions  ActionConfig
	patterns []*regexp.Regexp
}

func New(cfg Config) (*Extractor, error) {
	e := &Extractor{
		browser: cfg.Browser,
		capture: cfg.Capture,
		actions: cfg.Actions,
	}

	for i, p := range cfg.Capture.Patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("pattern #%d: %w", i, err)
		}
		e.patterns = append(e.patterns, re)
	}

	return e, nil
}

// extract runs a single session+pipeline extraction attempt.
func (e *Extractor) extract(ctx context.Context, targetURL string) ([]*media.Stream, error) {
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
			slog.DebugContext(ctx, "skipping entry, invalid URL", "raw_url", entry.RawURL, "error", err)
			continue
		}
		// Prefer the extension, fall back to the captured MIME type; both are
		// pure lookups, so cmp.Or's eager evaluation costs nothing.
		ct := cmp.Or(media.DetectFromExtension(u), media.DetectFromMIME(entry.MimeType))
		if ct == "" {
			slog.DebugContext(ctx, "skipping entry, unknown content type", "url", u.String())
			continue
		}
		streams = append(streams, &media.Stream{URL: u, Headers: media.NormalizeStreamHeaders(entry.Headers), ContentType: ct})
	}

	if len(streams) == 0 {
		return nil, fmt.Errorf("no usable streams found (%d entries captured, none with recognized content type)", len(entries))
	}

	return streams, nil
}

// ExtractAll runs Extract concurrently on all given URLs (bounded by the
// extractor's MaxConcurrency) and returns deduplicated streams.
func (e *Extractor) ExtractAll(ctx context.Context, urls []string) ([]*media.Stream, error) {
	slog.InfoContext(ctx, "extracting streams", "urls", len(urls))

	var wg sync.WaitGroup
	sem := make(chan struct{}, e.capture.MaxConcurrency)
	results := make([][]*media.Stream, len(urls))

	for i, targetURL := range urls {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			slog.DebugContext(ctx, "extracting", "url", targetURL, "index", i+1, "total", len(urls))

			streams, err := e.extract(ctx, targetURL)
			if err != nil {
				slog.WarnContext(ctx, "extraction failed", "url", targetURL, "error", err)
				return
			}

			results[i] = streams
			slog.DebugContext(ctx, "extracted", "url", targetURL, "count", len(streams))
		})
	}
	wg.Wait()

	var allStreams []*media.Stream
	for _, ss := range results {
		allStreams = append(allStreams, ss...)
	}

	deduped := deduplicateStreams(allStreams)
	slog.InfoContext(ctx, "extraction complete", "urls", len(urls), "streams", len(deduped))
	return deduped, nil
}

func deduplicateStreams(streams []*media.Stream) []*media.Stream {
	seen := make(map[string]struct{}, len(streams))
	return slices.DeleteFunc(slices.Clone(streams), func(s *media.Stream) bool {
		key := s.URL.String()
		if _, ok := seen[key]; ok {
			return true
		}
		seen[key] = struct{}{}
		return false
	})
}
