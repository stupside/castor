package extract

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
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

	"github.com/stupside/castor/services/scrapingserver/internal/streaminfo"
)

// urlInText finds the absolute links a page player prints to its console.
var urlInText = regexp.MustCompile(`https?://[^\s"'<>]+`)

// bodyReader asks the browser for the bytes of a response it already holds.
type bodyReader func(network.RequestID) ([]byte, error)

// capture is one link the page fetched that may be a stream, and what its body said once read.
type capture struct {
	raw         string
	url         *url.URL
	reqID       network.RequestID
	contentType string
	ladder      streaminfo.Ladder
	runtime     time.Duration
}

// collector is what one page fetched that may be a stream, and what its documents said.
type collector struct {
	// ctx is the extraction's: what the collector logs is told as part of it.
	ctx      context.Context
	readBody bodyReader
	grace    time.Duration
	window   time.Duration
	preRoll  time.Duration

	mu             sync.Mutex
	captures       []capture
	requestHeaders map[network.RequestID]http.Header
	bodyRead       map[network.RequestID]struct{}
	responseURL    map[network.RequestID]string
	// named contains the resources named by captured documents.
	named  map[string]struct{}
	closed bool
	reads  sync.WaitGroup

	// added is closed and replaced on every capture.
	added    chan struct{}
	mastered chan struct{}
}

func newCollector(ctx context.Context, readBody bodyReader, grace, window, preRoll time.Duration) *collector {
	return &collector{
		ctx:            ctx,
		readBody:       readBody,
		grace:          grace,
		window:         window,
		preRoll:        preRoll,
		requestHeaders: make(map[network.RequestID]http.Header),
		bodyRead:       make(map[network.RequestID]struct{}),
		responseURL:    make(map[network.RequestID]string),
		named:          make(map[string]struct{}),
		added:          make(chan struct{}),
		mastered:       make(chan struct{}),
	}
}

// addByURL records a link whose name says it is a segmented manifest.
func (c *collector) addByURL(raw string, reqID network.RequestID) {
	u, err := url.Parse(raw)
	if err != nil {
		return
	}
	if ct := streaminfo.ContentTypeOf(u, ""); streaminfo.IsSegmented(ct) {
		c.add(raw, u, reqID, ct)
	}
}

// untypedMIMETypes are what a store answers when it was never told what it holds.
var untypedMIMETypes = []string{"application/octet-stream", "binary/octet-stream"}

// addByMIME records a response whose server-confirmed MIME type is a stream type, or whose untyped response the link's name types.
func (c *collector) addByMIME(raw string, reqID network.RequestID, mime string) {
	untyped := slices.Contains(untypedMIMETypes, strings.ToLower(mime))
	if !untyped && streaminfo.ContentTypeOf(nil, mime) == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		return
	}
	if ct := streaminfo.ContentTypeOf(u, mime); ct != "" {
		c.add(raw, u, reqID, ct)
	}
}

func (c *collector) add(raw string, u *url.URL, reqID network.RequestID, contentType string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if i := c.index(raw); i >= 0 {
		if c.captures[i].reqID == "" && reqID != "" {
			c.captures[i].reqID = reqID
			slog.DebugContext(c.ctx, "attached request headers to captured URL", "url", raw)
		}
		return
	}
	if len(c.captures) >= maxCaptures {
		slog.DebugContext(c.ctx, "capture limit reached, skipping URL", "url", raw)
		return
	}

	slog.InfoContext(c.ctx, "captured stream", "url", raw, "content_type", contentType)
	c.captures = append(c.captures, capture{raw: raw, url: u, reqID: reqID, contentType: contentType})
	close(c.added)
	c.added = make(chan struct{})
}

func (c *collector) index(raw string) int {
	return slices.IndexFunc(c.captures, func(cp capture) bool { return cp.raw == raw })
}

// entries is every capture in the order it was made, less those another capture's document names.
func (c *collector) entries() []*streaminfo.Stream {
	c.mu.Lock()
	defer c.mu.Unlock()

	var out []*streaminfo.Stream
	for _, cp := range c.captures {
		if _, part := c.named[cp.raw]; part {
			continue
		}
		out = append(out, &streaminfo.Stream{
			URL: cp.url,
			// Normalized under the lock: the listener keeps merging into this map while the caller reads it.
			Headers:     replayable(c.requestHeaders[cp.reqID]),
			ContentType: cp.contentType,
			Ladder:      cp.ladder,
		})
	}
	return out
}

// hasHits reports whether anything at all was captured.
func (c *collector) hasHits() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.captures) > 0
}

// progress reports whether anything was captured, whether all of it is ads, and what closes on the next capture.
func (c *collector) progress() (hits, onlyAds bool, next <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	onlyAds = !slices.ContainsFunc(c.captures, func(cp capture) bool { return !streaminfo.ShorterThanContent(cp.runtime) })
	return len(c.captures) > 0, onlyAds, c.added
}

// hasMaster reports whether a document advertising renditions has been captured.
func (c *collector) hasMaster() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.ContainsFunc(c.captures, func(cp capture) bool { return cp.ladder == streaminfo.LadderMultivariant })
}

// askForDocument reads a finished response as a document, once, on a read the collector joins on close.
func (c *collector) askForDocument(reqID network.RequestID, size float64) {
	if !c.claimBodyRead(reqID, size) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.reads.Go(func() {
		body, err := c.readBody(reqID)
		if err != nil {
			slog.DebugContext(c.ctx, "response body unavailable, renditions unknown", "request", reqID, "error", err)
			return
		}
		c.noteDocument(reqID, string(body))
	})
}

// close waits out every body read in flight; none starts after it.
func (c *collector) close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.reads.Wait()
}

// documentSizeLimit is how large a response may be and still be worth reading as a document.
const documentSizeLimit = 2 << 20

// claimBodyRead decides whether one finished request is worth reading as a document and claims it.
func (c *collector) claimBodyRead(reqID network.RequestID, size float64) bool {
	if reqID == "" || size > documentSizeLimit {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, done := c.bodyRead[reqID]; done {
		return false
	}
	i := c.captureFor(reqID)
	if i < 0 || c.captures[i].ladder != streaminfo.LadderUnknown {
		return false
	}
	c.bodyRead[reqID] = struct{}{}
	return true
}

// noteResponseURL records where one request ended.
func (c *collector) noteResponseURL(id network.RequestID, u string) {
	if id == "" || u == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.responseURL[id] = u
}

// captureFor resolves the capture a finished request's body belongs to. Callers hold the lock.
func (c *collector) captureFor(reqID network.RequestID) int {
	if u := c.responseURL[reqID]; u != "" {
		if i := c.index(u); i >= 0 {
			return i
		}
	}
	return slices.IndexFunc(c.captures, func(cp capture) bool { return cp.reqID == reqID })
}

func (c *collector) noteDocument(reqID network.RequestID, body string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := c.captureFor(reqID)
	if i < 0 {
		return
	}
	// The URL the body was fetched from, which is what its references resolve against.
	base, err := url.Parse(cmp.Or(c.responseURL[reqID], c.captures[i].raw))
	if err != nil {
		return
	}
	doc := streaminfo.ParseDocument(body, base)
	c.captures[i].ladder = doc.Ladder
	c.captures[i].runtime = doc.Runtime
	for _, u := range doc.Names {
		if name := u.String(); name != c.captures[i].raw {
			c.named[name] = struct{}{}
		}
	}
	if doc.Ladder == streaminfo.LadderMultivariant {
		closeOnce(c.mastered)
	}
	slog.InfoContext(c.ctx, "read captured document", "url", c.captures[i].raw, "renditions", doc.Ladder, "names", len(doc.Names), "runtime", doc.Runtime)
}

// Wait gives the page its grace, then a window per capture to fetch a master, and while it holds only ads, its pre-roll.
func (c *collector) Wait(ctx context.Context) ([]*streaminfo.Stream, error) {
	preRoll := time.After(c.preRoll)
	if hits, _, next := c.progress(); !hits {
		select {
		case <-next:
		case <-time.After(c.grace):
		case <-ctx.Done():
		}
	}

	for c.hasHits() && ctx.Err() == nil {
		select {
		case <-c.mastered:
		case <-time.After(c.window):
		case <-ctx.Done():
		}
		_, onlyAds, next := c.progress()
		if !onlyAds {
			break
		}
		slog.InfoContext(ctx, "everything captured runs shorter than content; waiting for what the ads precede")
		select {
		case <-next:
			continue
		case <-preRoll:
		case <-ctx.Done():
		}
		break
	}

	if entries := c.entries(); len(entries) > 0 {
		return entries, nil
	}
	return nil, errors.New("no stream URL captured within grace period")
}

func (c *collector) listen(ev any) {
	switch e := ev.(type) {
	case *network.EventRequestWillBeSent:
		// Page-set headers only.
		c.mergeHeaders(e.RequestID, toHTTPHeader(e.Request.Headers))
		c.addByURL(e.Request.URL, e.RequestID)

	case *network.EventRequestWillBeSentExtraInfo:
		// The real on-the-wire headers (Referer, Origin, Cookie, sec-ch-*).
		c.mergeHeaders(e.RequestID, toHTTPHeader(e.Headers))

	case *network.EventResponseReceived:
		c.noteResponseURL(e.RequestID, e.Response.URL)
		c.addByMIME(e.Response.URL, e.RequestID, e.Response.MimeType)

	case *network.EventLoadingFinished:
		// This event, and not responseReceived, is when a body read is worth attempting.
		c.askForDocument(e.RequestID, e.EncodedDataLength)

	case *runtime.EventConsoleAPICalled:
		for _, arg := range e.Args {
			// A printed string arrives JSON-encoded, its own quotes escaped.
			var text string
			if json.Unmarshal(arg.Value, &text) != nil {
				continue
			}
			for _, raw := range urlInText.FindAllString(text, -1) {
				c.addByURL(raw, "")
			}
		}
	}
}

func closeOnce(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}
