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
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"

	"github.com/stupside/castor/internal/media"
)

// hlsURLPattern matches HTTP(S) URLs containing .m3u8 in console output.
var hlsURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+\.m3u8[^\s"'<>]*`)

type capturedStream struct {
	RawURL   string
	Headers  http.Header
	MimeType string // confirmed by server; empty if only URL-pattern matched
}

type candidate struct {
	score    int
	reqID    network.RequestID
	rawURL   string
	mimeType string
}

// collector captures and deduplicates stream URLs from browser events.
type collector struct {
	ctx            context.Context
	patterns       []*regexp.Regexp
	maxCandidates  int
	mu             sync.Mutex
	candidates     []candidate
	requestHeaders map[network.RequestID]http.Header
	notify         chan struct{} // closed on first capture
}

func newCollector(ctx context.Context, patterns []*regexp.Regexp, maxCandidates int) *collector {
	return &collector{
		ctx:            ctx,
		patterns:       patterns,
		maxCandidates:  maxCandidates,
		requestHeaders: make(map[network.RequestID]http.Header),
		notify:         make(chan struct{}),
	}
}

// addByPattern records a URL if it matches capture patterns.
func (c *collector) addByPattern(u string, reqID network.RequestID) {
	if !matchesPattern(u, c.patterns) {
		return
	}
	c.add(u, reqID, "")
}

// addByMIME records a URL when the server has confirmed the MIME type is a
// stream type. Pattern matching is skipped — the confirmed MIME takes precedence.
func (c *collector) addByMIME(u string, reqID network.RequestID, mime string) {
	if media.DetectFromMIME(mime) == "" {
		return
	}
	c.add(u, reqID, strings.ToLower(mime))
}

// add deduplicates and appends a URL. On a duplicate it enriches the existing
// candidate in place rather than adding a second row.
func (c *collector) add(u string, reqID network.RequestID, mimeType string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if i := slices.IndexFunc(c.candidates, func(cand candidate) bool { return cand.rawURL == u }); i >= 0 {
		// Already captured. A console-first URL has no request ID (so no
		// headers); adopting a later network sighting's ID gives hotlink-
		// protected hosts the Referer/Origin they need instead of a 403. Fill in
		// a MIME confirmed later the same way.
		if c.candidates[i].reqID == "" && reqID != "" {
			c.candidates[i].reqID = reqID
			slog.DebugContext(c.ctx, "attached request headers to captured URL", "url", u)
		}
		if c.candidates[i].mimeType == "" && mimeType != "" {
			c.candidates[i].mimeType = mimeType
		}
		return
	}

	if len(c.candidates) >= c.maxCandidates {
		slog.DebugContext(c.ctx, "max candidates reached, skipping URL", "url", u)
		return
	}

	slog.InfoContext(c.ctx, "captured stream", "url", u, "mime", mimeType)

	c.candidates = append(c.candidates, candidate{
		rawURL:   u,
		reqID:    reqID,
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

// Entries returns captured streams sorted by score (descending), each with its
// outgoing headers resolved from the merged per-request header set.
func (c *collector) Entries() []capturedStream {
	c.mu.Lock()
	defer c.mu.Unlock()

	sorted := slices.SortedFunc(slices.Values(c.candidates), func(a, b candidate) int {
		return cmp.Compare(b.score, a.score)
	})
	entries := make([]capturedStream, len(sorted))
	for i, cand := range sorted {
		entries[i] = capturedStream{
			RawURL:   cand.rawURL,
			Headers:  c.requestHeaders[cand.reqID], // nil reqID → nil headers (console-only)
			MimeType: cand.mimeType,
		}
	}
	return entries
}

func (c *collector) HasHits() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.candidates) > 0
}

// hasMaster reports whether a master playlist has been captured. A master is
// the top of the HLS tree, so once one is seen there's nothing better to wait
// for and the collection window can be cut short.
func (c *collector) hasMaster() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.ContainsFunc(c.candidates, func(cand candidate) bool {
		return cand.score >= masterScore
	})
}

// Wait blocks until streams are found or the grace period expires.
// If streams are already captured, it waits collectionWindow for more, then returns.
// If no streams are found, it waits up to graceAfterActions before giving up.
func (c *collector) Wait(ctx context.Context, graceAfterActions, collectionWindow time.Duration) ([]capturedStream, error) {
	collectMore := func() []capturedStream {
		// Once a master playlist is in hand there's nothing better to wait for —
		// it already enumerates every variant — so return immediately instead of
		// burning the rest of collectionWindow. These source URLs are short-lived
		// signed links; every second spent here is a second of the token's life
		// gone before the puller can touch it. Without a master we keep collecting
		// (a late master or fallback variant may still arrive) up to the window.
		timer := time.NewTimer(collectionWindow)
		defer timer.Stop()
		poll := time.NewTicker(100 * time.Millisecond)
		defer poll.Stop()
		for {
			if c.hasMaster() {
				return c.Entries()
			}
			select {
			case <-timer.C:
				return c.Entries()
			case <-poll.C:
			case <-ctx.Done():
				return c.Entries()
			}
		}
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
		// Page-set headers only. The browser-added Referer/Origin/Cookie that
		// hotlinked CDNs check arrive separately, in the ExtraInfo event below;
		// both merge by request ID.
		c.mergeHeaders(e.RequestID, toHTTPHeader(e.Request.Headers))
		c.addByPattern(e.Request.URL, e.RequestID)

	case *network.EventRequestWillBeSentExtraInfo:
		// The real on-the-wire headers (Referer, Origin, Cookie, sec-ch-*).
		c.mergeHeaders(e.RequestID, toHTTPHeader(e.Headers))

	case *network.EventResponseReceived:
		c.addByMIME(e.Response.URL, e.RequestID, e.Response.MimeType)

	case *runtime.EventConsoleAPICalled:
		for _, arg := range e.Args {
			val := strings.Trim(string(arg.Value), `"`)
			for _, m := range hlsURLPattern.FindAllString(val, -1) {
				c.addByPattern(m, "")
			}
		}
	}
}

// mergeHeaders folds outgoing headers into the set recorded for a request ID.
// requestWillBeSent and ExtraInfo each call it once, in either order; non-empty
// values win so neither clobbers the other's contribution.
func (c *collector) mergeHeaders(id network.RequestID, headers http.Header) {
	if id == "" || len(headers) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	existing := c.requestHeaders[id]
	if existing == nil {
		existing = make(http.Header, len(headers))
		c.requestHeaders[id] = existing
	}
	for k, vs := range headers {
		if len(vs) > 0 && vs[0] != "" {
			existing[k] = vs
		}
	}
}

// toHTTPHeader converts CDP headers into an http.Header (canonical keys),
// skipping HTTP/2 pseudo-headers (":authority", ":method", …) that an outgoing
// request can't carry. Nil when nothing usable remains.
func toHTTPHeader(h network.Headers) http.Header {
	out := make(http.Header, len(h))
	for k, v := range h {
		if s, ok := v.(string); ok && !strings.HasPrefix(k, ":") {
			out.Set(k, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

// masterScore is the rankURL bonus for a path containing "master". A candidate
// scoring at least this high is a master playlist: the variant penalty can't
// drag a non-master candidate up to it, so the threshold cleanly identifies one.
const masterScore = 100

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
		score += masterScore
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
