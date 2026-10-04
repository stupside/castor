package extract

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/chromedp/cdproto/network"
)

// replayable is a copy of headers the browser sent, ready for another reader to send.
func replayable(h http.Header) http.Header {
	out := h.Clone()
	if out == nil {
		return nil
	}
	out.Del("Range")
	out.Del("Accept-Encoding")
	if out.Get("Origin") == "" {
		if origin := originOf(out.Get("Referer")); origin != "" {
			out.Set("Origin", origin)
		}
	}
	return out
}

// originOf returns the scheme://host origin of an absolute URL, or "" if s is not one.
func originOf(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}

// mergeHeaders folds outgoing headers into the set recorded for a request ID.
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

// toHTTPHeader keeps what a reader can send: HTTP/2 pseudo-headers (:authority, :path) are the connection's, not the request's.
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
