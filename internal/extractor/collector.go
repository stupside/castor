package extractor

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
)

// streamMIMETypes are MIME types that indicate a streaming response.
var streamMIMETypes = map[string]bool{
	"audio/mpegurl":                 true,
	"audio/x-mpegurl":               true,
	"application/x-mpegurl":         true,
	"application/vnd.apple.mpegurl": true,
	"video/mp4":                     true,
	"video/webm":                    true,
	"video/x-matroska":              true,
}

// hlsURLPattern matches HTTP(S) URLs containing .m3u8 in console output.
var hlsURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+\.m3u8[^\s"'<>]*`)

// capturedStream holds a captured URL and the HTTP headers from its request.
type capturedStream struct {
	RawURL   string
	Headers  map[string]string
	MimeType string // confirmed by server; empty if only URL-pattern matched
}

// candidate is a captured stream URL with a score for ranking.
type candidate struct {
	rawURL   string
	headers  map[string]string
	mimeType string
	score    int
}

// collector captures and deduplicates stream URLs from browser events.
type collector struct {
	patterns      []*regexp.Regexp
	maxCandidates int
	mu            sync.Mutex
	candidates    []candidate
	notify        chan struct{} // closed on first capture
}

// newCollector creates a collector for the given capture patterns.
func newCollector(patterns []*regexp.Regexp, maxCandidates int) *collector {
	return &collector{
		patterns:      patterns,
		maxCandidates: maxCandidates,
		notify:        make(chan struct{}),
	}
}

// Add records a URL if it matches capture patterns, is not a duplicate,
// and the candidate list is not full.
func (c *collector) Add(u string, headers map[string]string) {
	if !matchesPattern(u, c.patterns) {
		slog.Debug("collector: URL did not match patterns", "url", u)
		return
	}
	c.add(u, headers, "")
}

// AddByMIME records a URL when the server has confirmed the MIME type is a
// stream type. Pattern matching is skipped — the confirmed MIME takes precedence.
func (c *collector) AddByMIME(u string, mime string, headers map[string]string) {
	if !streamMIMETypes[strings.ToLower(mime)] {
		slog.Debug("collector: MIME type not a stream type", "url", u, "mime", mime)
		return
	}
	c.add(u, headers, strings.ToLower(mime))
}

// add deduplicates and appends a URL without pattern checking.
func (c *collector) add(u string, headers map[string]string, mimeType string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.candidates) >= c.maxCandidates {
		slog.Debug("collector: max candidates reached, skipping URL", "url", u)
		return
	}

	if slices.ContainsFunc(c.candidates, func(cand candidate) bool { return cand.rawURL == u }) {
		slog.Debug("collector: duplicate URL, skipping", "url", u)
		return
	}

	c.candidates = append(c.candidates, candidate{
		rawURL:   u,
		headers:  headers,
		mimeType: mimeType,
		score:    rankURL(u),
	})

	// Signal waiters on first capture.
	select {
	case <-c.notify:
	default:
		close(c.notify)
	}
}

// Entries returns captured streams sorted by score (descending).
func (c *collector) Entries() []capturedStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	return sortedEntries(c.candidates)
}

// HasHits returns true if any candidates have been captured.
func (c *collector) HasHits() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.candidates) > 0
}

// Wait blocks until streams are found or the grace period expires.
// If streams are already captured, it waits collectionWindow for more, then returns.
// If no streams are found, it waits up to graceAfterActions before giving up.
func (c *collector) Wait(ctx context.Context, graceAfterActions, collectionWindow time.Duration) ([]capturedStream, error) {
	collectMore := func() []capturedStream {
		timer := time.NewTimer(collectionWindow)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
		return c.Entries()
	}

	if entries := c.Entries(); len(entries) > 0 {
		return collectMore(), nil
	}

	graceCtx, graceCancel := context.WithTimeout(ctx, graceAfterActions)
	defer graceCancel()

	select {
	case <-c.notify:
		return collectMore(), nil
	case <-graceCtx.Done():
		if entries := c.Entries(); len(entries) > 0 {
			return entries, nil
		}
		return nil, fmt.Errorf("no stream URL captured within grace period")
	}
}

// Listen returns an event handler for chromedp.ListenTarget that feeds
// network requests and console messages into the collector.
func (c *collector) Listen(ev any) {
	switch e := ev.(type) {
	case *network.EventRequestWillBeSent:
		c.Add(e.Request.URL, networkHeadersToMap(e.Request.Headers))

	case *network.EventResponseReceived:
		c.AddByMIME(e.Response.URL, e.Response.MimeType, networkHeadersToMap(responseRequestHeaders(e.Response)))

	case *runtime.EventConsoleAPICalled:
		for _, arg := range e.Args {
			val := strings.Trim(string(arg.Value), `"`)
			for _, m := range hlsURLPattern.FindAllString(val, -1) {
				c.Add(m, nil)
			}
		}
	}
}

// responseRequestHeaders returns the request headers from a response, falling
// back to the response headers when RequestHeaders is not populated by Chrome.
func responseRequestHeaders(r *network.Response) network.Headers {
	if len(r.RequestHeaders) > 0 {
		return r.RequestHeaders
	}
	return r.Headers
}

// networkHeadersToMap converts chromedp network.Headers (map[string]any) to map[string]string.
func networkHeadersToMap(h network.Headers) map[string]string {
	if len(h) == 0 {
		return nil
	}
	m := make(map[string]string, len(h))
	for k, v := range h {
		if s, ok := v.(string); ok {
			m[k] = s
		}
	}
	return m
}

// matchesPattern checks if a URL matches any of the capture patterns.
// Query parameters are stripped so encoded URLs in tracking pixels don't match.
func matchesPattern(u string, patterns []*regexp.Regexp) bool {
	stripped, _, _ := strings.Cut(u, "?")
	for _, re := range patterns {
		if re.MatchString(stripped) {
			return true
		}
	}
	return false
}

// variantPatterns are URL path substrings that indicate a variant/segment
// rather than a master playlist.
var variantPatterns = []string{
	"/720p/", "/1080p/", "/480p/", "/360p/", "/240p/",
	"/chunklist", "/media-", "/segment",
}

// rankURL assigns a score to a captured URL for quality/variant selection.
// Higher score = more preferred (master playlists over variants).
func rankURL(rawURL string) int {
	score := 0

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return score
	}

	p := strings.ToLower(parsed.Path)

	if strings.Contains(p, "master") {
		score += 100
	}
	if strings.Contains(p, "playlist") {
		score += 50
	}

	if slices.ContainsFunc(variantPatterns, func(vp string) bool {
		return strings.Contains(p, vp)
	}) {
		score -= 50
	}

	return score
}

// sortedEntries sorts candidates by score descending and returns CapturedStream entries.
func sortedEntries(candidates []candidate) []capturedStream {
	sorted := make([]candidate, len(candidates))
	copy(sorted, candidates)
	slices.SortFunc(sorted, func(a, b candidate) int {
		return cmp.Compare(b.score, a.score)
	})
	entries := make([]capturedStream, len(sorted))
	for i, c := range sorted {
		entries[i] = capturedStream{
			RawURL:   c.rawURL,
			Headers:  c.headers,
			MimeType: c.mimeType,
		}
	}
	return entries
}
