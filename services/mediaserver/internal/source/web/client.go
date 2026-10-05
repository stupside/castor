// Package web is the HTTP client castor reads origins with: documents, media, and the session they share.
package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// client reads an origin over net/http with the session its documents opened; satisfies source.Client.
type client struct {
	http *http.Client
}

// documentLimit is far above any real playlist or manifest, and far below a film served in its place.
const documentLimit = 16 << 20

// New reads origins with documents bounded by timeout, sharing one cookie session across every read.
func New(timeout time.Duration) source.Client {
	// A CDN that authorises a session on the master (Akamai's hdntl) refuses every later read without its cookie.
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return &client{http: &http.Client{Timeout: timeout, Jar: jar}}
}

// Replay is h with the jar's cookies for u winning over a copy h took earlier: the jar holds what refreshed during resolution.
func (c *client) Replay(u *url.URL, h http.Header) http.Header {
	jarred := c.http.Jar.Cookies(u)
	if len(jarred) == 0 {
		return h
	}
	stated := (&http.Request{Header: h}).Cookies()
	cookies := slices.Concat(slices.DeleteFunc(stated, jarredIn(jarred)), jarred)
	pairs := make([]string, len(cookies))
	for i, cookie := range cookies {
		pairs[i] = cookie.Name + "=" + cookie.Value
	}
	out := h.Clone()
	if out == nil {
		out = http.Header{}
	}
	out.Set("Cookie", strings.Join(pairs, "; "))
	return out
}

func (c *client) Fetch(ctx context.Context, u *url.URL, h http.Header) (string, *url.URL, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", u, 0, fmt.Errorf("creating request: %w", err)
	}
	maps.Copy(req.Header, h)
	withoutJarred(req, c.http.Jar.Cookies(u))

	resp, err := c.http.Do(req)
	if err != nil {
		return "", u, 0, fmt.Errorf("fetching document: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	from := u
	if resp.Request != nil && resp.Request.URL != nil {
		from = resp.Request.URL
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", from, resp.StatusCode, fmt.Errorf("fetching document: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, documentLimit+1))
	if err != nil {
		return "", from, resp.StatusCode, fmt.Errorf("reading document: %w", err)
	}
	if len(body) > documentLimit {
		return "", from, resp.StatusCode, fmt.Errorf("reading document: larger than %d bytes", documentLimit)
	}
	return string(body), from, resp.StatusCode, nil
}

// Read opens media bytes with the headers and session a document read carries, whole or within r.
func (c *client) Read(ctx context.Context, u *url.URL, h http.Header, r timeline.Range) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	maps.Copy(req.Header, h)
	withoutJarred(req, c.http.Jar.Cookies(u))
	if r.Length > 0 {
		req.Header.Set("Range", r.Header())
	}
	// Media is read for as long as it takes to arrive, bounded by the caller rather than the document timeout.
	reader := *c.http
	reader.Timeout = 0
	resp, err := reader.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching media: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, &timeline.Failure{Status: resp.StatusCode, Err: errors.New("fetching media")}
	}
	if r.Length > 0 && resp.StatusCode == http.StatusPartialContent {
		var first, last int64
		if _, err := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/", &first, &last); err != nil || first != r.Offset || last != r.Offset+r.Length-1 {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("fetching media: Content-Range %q does not match %s", resp.Header.Get("Content-Range"), r.Header())
		}
	}
	// An origin that ignores Range answers the whole resource, of which only r is wanted.
	if r.Length > 0 && resp.StatusCode == http.StatusOK {
		if _, err := io.CopyN(io.Discard, resp.Body, r.Offset); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("skipping to byte %d: %w", r.Offset, err)
		}
	}
	if r.Length > 0 {
		return &rangeReader{ReadCloser: resp.Body, remaining: r.Length}, nil
	}
	return resp.Body, nil
}

// rangeReader stops at the requested length and reports a short origin body as truncated media.
type rangeReader struct {
	io.ReadCloser
	remaining int64
}

func (r *rangeReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.ReadCloser.Read(p)
	r.remaining -= int64(n)
	if errors.Is(err, io.EOF) && r.remaining > 0 {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

// withoutJarred drops from a request's Cookie header each name the jar will send itself, fresher than a copy taken earlier.
func withoutJarred(req *http.Request, jarred []*http.Cookie) {
	stated := req.Cookies()
	if len(stated) == 0 || len(jarred) == 0 {
		return
	}
	req.Header.Del("Cookie")
	for _, c := range slices.DeleteFunc(stated, jarredIn(jarred)) {
		req.AddCookie(c)
	}
}

// jarredIn reports a cookie the jar holds one of the same name for.
func jarredIn(jarred []*http.Cookie) func(*http.Cookie) bool {
	return func(c *http.Cookie) bool {
		return slices.ContainsFunc(jarred, func(j *http.Cookie) bool { return j.Name == c.Name })
	}
}
