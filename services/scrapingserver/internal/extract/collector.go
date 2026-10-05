package extract

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// urlInText finds the absolute links a page player prints to its console.
var urlInText = regexp.MustCompile(`https?://[^\s"'<>]+`)

// bodyReader asks the browser for the bytes of a response it already holds.
type bodyReader func(network.RequestID) ([]byte, error)

// capture is one link the page fetched that may be a stream, and what its body said once read.
type capture struct {
	raw         string
	reqID       network.RequestID
	contentType string
	ladder      castorv1.Ladder
	runtime     time.Duration
	hop         *requestHop
}

type requestHop struct {
	url           string
	headers       http.Header
	wireHeaders   http.Header
	extraKnown    bool
	extraExpected bool
}

type requestState struct {
	hops        []*requestHop
	extras      []http.Header
	responseURL string
	bodyRead    bool
}

const (
	maxTrackedRequests = 2048
	maxRequestHops     = 20
	maxNamedResources  = 8192
	maxDocumentReads   = 4
)

// collector is what one page fetched that may be a stream, and what its documents said.
type collector struct {
	// ctx is the extraction's: what the collector logs is told as part of it.
	ctx      context.Context
	readBody bodyReader
	grace    time.Duration
	window   time.Duration
	preRoll  time.Duration

	mu          sync.Mutex
	captures    []capture
	requests    map[network.RequestID]*requestState
	activeReads int
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
		ctx:      ctx,
		readBody: readBody,
		grace:    grace,
		window:   window,
		preRoll:  preRoll,
		requests: make(map[network.RequestID]*requestState),
		named:    make(map[string]struct{}),
		added:    make(chan struct{}),
		mastered: make(chan struct{}),
	}
}

// addByURL records a link whose name says it is a segmented manifest.
func (c *collector) addByURL(raw string, reqID network.RequestID) {
	if len(raw) > 8192 || !webPage(raw) {
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		return
	}
	if ct := contentTypeOf(u, ""); segmented(ct) {
		c.add(raw, reqID, ct)
	}
}

// untypedMIMETypes are what a store answers when it was never told what it holds.
var untypedMIMETypes = []string{"application/octet-stream", "binary/octet-stream"}

// addByMIME records a response whose server-confirmed MIME type is a stream type, or whose untyped response the link's name types.
func (c *collector) addByMIME(raw string, reqID network.RequestID, mime string) {
	if len(raw) > 8192 || !webPage(raw) {
		return
	}
	untyped := slices.Contains(untypedMIMETypes, strings.ToLower(mime))
	if !untyped && contentTypeOf(nil, mime) == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		return
	}
	if ct := contentTypeOf(u, mime); ct != "" {
		c.add(raw, reqID, ct)
	}
}

func (c *collector) add(raw string, reqID network.RequestID, contentType string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	var hop *requestHop
	if req := c.request(reqID, true); req != nil {
		if len(req.hops) == 0 {
			req.hops = append(req.hops, &requestHop{url: raw})
		}
		last := req.hops[len(req.hops)-1]
		if last.url == raw {
			hop = last
		}
	}

	if i := c.index(raw); i >= 0 {
		if reqID != "" {
			c.captures[i].reqID = reqID
			c.captures[i].hop = hop
			slog.DebugContext(c.ctx, "attached request headers to captured URL", "url", raw)
		}
		return
	}
	if len(c.captures) >= maxCaptures {
		slog.DebugContext(c.ctx, "capture limit reached, skipping URL", "url", raw)
		return
	}

	slog.InfoContext(c.ctx, "captured stream", "url", raw, "content_type", contentType)
	c.captures = append(c.captures, capture{raw: raw, reqID: reqID, contentType: contentType, hop: hop})
	close(c.added)
	c.added = make(chan struct{})
}

func (c *collector) index(raw string) int {
	return slices.IndexFunc(c.captures, func(cp capture) bool { return cp.raw == raw })
}

// entries is every capture in the order it was made, less those another capture's document names.
func (c *collector) entries() []*castorv1.StreamCandidate {
	c.mu.Lock()
	defer c.mu.Unlock()

	var out []*castorv1.StreamCandidate
	for _, cp := range c.captures {
		if _, part := c.named[cp.raw]; part {
			continue
		}
		out = append(out, &castorv1.StreamCandidate{
			// The listener keeps merging headers while callers read earlier snapshots.
			Stream: &castorv1.Stream{Url: cp.raw, Headers: replayable(hopHeaders(cp.hop)), ContentType: cp.contentType},
			Ladder: cp.ladder,
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
	onlyAds = !slices.ContainsFunc(c.captures, func(cp capture) bool { return cp.runtime <= 0 || cp.runtime >= 5*time.Minute })
	return len(c.captures) > 0, onlyAds, c.added
}

// hasMaster reports whether a document advertising renditions has been captured.
func (c *collector) hasMaster() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.ContainsFunc(c.captures, func(cp capture) bool { return cp.ladder == castorv1.Ladder_LADDER_MULTIVARIANT })
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
	if c.activeReads >= maxDocumentReads {
		return
	}
	c.activeReads++
	c.reads.Go(func() {
		defer func() {
			c.mu.Lock()
			c.activeReads--
			c.mu.Unlock()
		}()
		body, err := c.readBody(reqID)
		if err != nil {
			slog.DebugContext(c.ctx, "response body unavailable, renditions unknown", "request", reqID, "error", err)
			return
		}
		if len(body) <= documentSizeLimit {
			c.noteDocument(reqID, string(body))
		}
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
	if reqID == "" || size < 0 || math.IsNaN(size) || size > documentSizeLimit {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	req := c.requests[reqID]
	if req == nil || req.bodyRead {
		return false
	}
	i := c.captureFor(reqID)
	if c.closed || i < 0 || !segmented(c.captures[i].contentType) || c.captures[i].ladder != castorv1.Ladder_LADDER_UNSPECIFIED {
		return false
	}
	req.bodyRead = true
	return true
}

// captureFor resolves the capture a finished request's body belongs to. Callers hold the lock.
func (c *collector) captureFor(reqID network.RequestID) int {
	if req := c.requests[reqID]; req != nil && req.responseURL != "" {
		u := req.responseURL
		if i := c.index(u); i >= 0 {
			return i
		}
	}
	return slices.IndexFunc(c.captures, func(cp capture) bool { return cp.reqID == reqID })
}

func (c *collector) noteDocument(reqID network.RequestID, body string) {
	if len(body) > documentSizeLimit {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	i := c.captureFor(reqID)
	if i < 0 {
		return
	}
	// The URL the body was fetched from, which is what its references resolve against.
	responseURL := ""
	if req := c.requests[reqID]; req != nil {
		responseURL = req.responseURL
	}
	base, err := url.Parse(cmp.Or(responseURL, c.captures[i].raw))
	if err != nil {
		return
	}
	doc := inspectDocument(body, base)
	c.captures[i].ladder = doc.ladder
	c.captures[i].runtime = doc.runtime
	for _, u := range doc.references {
		if name := u.String(); name != c.captures[i].raw && len(c.named) < maxNamedResources {
			c.named[name] = struct{}{}
		}
	}
	if doc.ladder == castorv1.Ladder_LADDER_MULTIVARIANT {
		closeOnce(c.mastered)
	}
	slog.InfoContext(c.ctx, "read captured document", "url", c.captures[i].raw, "renditions", doc.ladder, "names", len(doc.references), "runtime", doc.runtime)
}

// Wait gives the page its grace, then a window per capture to fetch a master, and while it holds only ads, its pre-roll.
func (c *collector) Wait(ctx context.Context) ([]*castorv1.StreamCandidate, error) {
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

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if entries := c.entries(); len(entries) > 0 {
		return entries, nil
	}
	return nil, errors.New("no stream URL captured within grace period")
}

func (c *collector) listen(ev any) {
	switch e := ev.(type) {
	case *network.EventRequestWillBeSent:
		c.noteRequest(e)
		c.addByURL(e.Request.URL, e.RequestID)

	case *network.EventRequestWillBeSentExtraInfo:
		// The real on-the-wire headers (Referer, Origin, Cookie, sec-ch-*).
		c.noteExtraHeaders(e.RequestID, toHTTPHeader(e.Headers))

	case *network.EventResponseReceived:
		c.addByMIME(e.Response.URL, e.RequestID, e.Response.MimeType)
		c.noteResponse(e)

	case *network.EventLoadingFinished:
		// This event, and not responseReceived, is when a body read is worth attempting.
		c.askForDocument(e.RequestID, e.EncodedDataLength)
		c.forgetUncaptured(e.RequestID)

	case *network.EventLoadingFailed:
		c.forgetUncaptured(e.RequestID)

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
