package extract

import (
	"cmp"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/chromedp/cdproto/network"
	"golang.org/x/net/http/httpguts"
)

// replayable is a copy of headers the browser sent, ready for another reader to send.
func replayable(h http.Header) map[string]string {
	out := h.Clone()
	if out == nil {
		return nil
	}
	out.Del("Range")
	out.Del("Accept-Encoding")
	// A replay has its own connection, body framing, and cache state.
	for _, value := range out.Values("Connection") {
		for field := range strings.SplitSeq(value, ",") {
			out.Del(strings.TrimSpace(field))
		}
	}
	for _, field := range []string{"Host", "Connection", "Proxy-Connection", "Keep-Alive", "Transfer-Encoding", "Te", "Trailer", "Upgrade", "Content-Length", "If-Range", "If-None-Match", "If-Modified-Since", "If-Match", "If-Unmodified-Since"} {
		out.Del(field)
	}
	if out.Get("Origin") == "" {
		if origin := originOf(out.Get("Referer")); origin != "" {
			out.Set("Origin", origin)
		}
	}
	// Preserve authentication and browser identity first if a page supplies
	// more headers than the stream contract can carry.
	keys := slices.Sorted(maps.Keys(out))
	keys = append([]string{"Cookie", "Authorization", "Referer", "Origin", "User-Agent", "Accept"}, keys...)
	bounded := make(map[string]string)
	for _, key := range keys {
		if values := out.Values(key); len(values) > 0 && strings.Join(values, "") != "" && len(bounded) < 64 {
			separator := ", "
			if strings.EqualFold(key, "Cookie") {
				separator = "; "
			}
			bounded[key] = strings.Join(values, separator)
		}
	}
	return bounded
}

// originOf returns the scheme://host origin of an absolute URL, or "" if s is not one.
func originOf(s string) string {
	u, err := url.Parse(s)
	if err != nil || !webPage(s) {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}

// request returns bounded request bookkeeping. Captured resources can replace
// irrelevant traffic when a page fills the budget. The caller holds c.mu.
func (c *collector) request(id network.RequestID, captured bool) *requestState {
	if id == "" || c.closed {
		return nil
	}
	if req := c.requests[id]; req != nil {
		return req
	}
	if len(c.requests) >= maxTrackedRequests {
		if !captured {
			return nil
		}
		for other := range c.requests {
			if !slices.ContainsFunc(c.captures, func(cp capture) bool { return cp.reqID == other }) {
				delete(c.requests, other)
				break
			}
		}
		if len(c.requests) >= maxTrackedRequests {
			return nil
		}
	}
	req := new(requestState)
	c.requests[id] = req
	return req
}

func (c *collector) noteRequest(e *network.EventRequestWillBeSent) {
	if len(e.Request.URL) > 8192 || !webPage(e.Request.URL) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	u, _ := url.Parse(e.Request.URL)
	req := c.request(e.RequestID, segmented(contentTypeOf(u, "")))
	if req == nil {
		return
	}
	if len(req.hops) > 0 {
		previous := req.hops[len(req.hops)-1]
		previous.extraKnown = true
		previous.extraExpected = e.RedirectHasExtraInfo
	}
	if len(req.hops) >= maxRequestHops {
		return
	}
	req.hops = append(req.hops, &requestHop{url: e.Request.URL, headers: toHTTPHeader(e.Request.Headers)})
	req.assignExtras()
}

func (c *collector) noteExtraHeaders(id network.RequestID, headers http.Header) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if req := c.request(id, false); req != nil && len(req.extras) < maxRequestHops {
		req.extras = append(req.extras, headers)
		req.assignExtras()
	}
}

func (c *collector) noteResponse(e *network.EventResponseReceived) {
	c.mu.Lock()
	defer c.mu.Unlock()
	req := c.requests[e.RequestID]
	if req == nil || c.closed {
		return
	}
	if len(e.Response.URL) <= 8192 && webPage(e.Response.URL) {
		req.responseURL = e.Response.URL
	}
	if len(req.hops) > 0 {
		hop := req.hops[len(req.hops)-1]
		hop.extraKnown, hop.extraExpected = true, e.HasExtraInfo
		if len(e.Response.RequestHeaders) > 0 {
			hop.headers = toHTTPHeader(e.Response.RequestHeaders)
		}
	}
	req.assignExtras()
}

// Extra-info events can precede their request and can arrive after a redirect.
// The response flags identify which hops emitted them; their order identifies
// the corresponding hop without mixing credentials between hosts.
func (r *requestState) assignExtras() {
	next := 0
	for _, hop := range r.hops {
		hop.wireHeaders = nil
		if hop.extraKnown && !hop.extraExpected {
			continue
		}
		if next < len(r.extras) {
			hop.wireHeaders = r.extras[next]
		}
		next++
	}
}

func hopHeaders(hop *requestHop) http.Header {
	if hop == nil {
		return nil
	}
	h := hop.headers.Clone()
	if h == nil {
		h = make(http.Header)
	}
	maps.Copy(h, hop.wireHeaders)
	return h
}

func (c *collector) forgetUncaptured(id network.RequestID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !slices.ContainsFunc(c.captures, func(cp capture) bool { return cp.reqID == id }) {
		delete(c.requests, id)
	}
}

// toHTTPHeader keeps what a reader can send: HTTP/2 pseudo-headers (:authority, :path) are the connection's, not the request's.
func toHTTPHeader(h network.Headers) http.Header {
	out := make(http.Header, min(len(h), 64))
	bytes := 0
	priority := func(name string) int {
		for i, field := range []string{"Cookie", "Authorization", "Referer", "Origin", "User-Agent", "Accept", "Connection"} {
			if strings.EqualFold(name, field) {
				return i
			}
		}
		return 7
	}
	keys := slices.Sorted(maps.Keys(h))
	// Apply the budget after keeping playback credentials and connection exclusions, regardless of header casing.
	slices.SortStableFunc(keys, func(a, b string) int { return cmp.Compare(priority(a), priority(b)) })
	for _, k := range keys {
		v := h[k]
		if s, ok := v.(string); ok && httpguts.ValidHeaderFieldName(k) && httpguts.ValidHeaderFieldValue(s) && bytes+len(k)+len(s) <= 8192 && len(out) < 64 {
			out.Set(k, s)
			bytes += len(k) + len(s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
