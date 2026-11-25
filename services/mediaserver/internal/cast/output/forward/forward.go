// Package forward serves a device the source's own bytes, fetched from the origin per request, so the device seeks what the origin serves.
package forward

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// Name is the path a forwarded source is published at, with the extension renderers sniff before they read a byte.
func Name(contentType string) string {
	if ext, ok := extensions[contentType]; ok {
		return "source" + ext
	}
	return "source"
}

var extensions = map[string]string{
	"video/x-matroska": ".mkv",
	"video/mp4":        ".mp4",
	"video/webm":       ".webm",
	"video/mp2t":       ".ts",
}

// Transport bounds the wait for the origin's answer only: a forwarded body streams for as long as the device reads.
func Transport() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 30 * time.Second
	return t
}

// Handler answers GET and HEAD at one path with what source answers for the same byte range.
// served are the device's own response headers; the origin's range support and length override them.
func Handler(client *http.Client, source *url.URL, headers http.Header, contentType string, served map[string]string) http.Handler {
	return handler(client, source, headers, contentType, served, false)
}

// Redirect answers HEAD with the origin's file metadata. GET goes to the fixed origin URL, including for byte ranges.
func Redirect(client *http.Client, source *url.URL, contentType string, served map[string]string) http.Handler {
	return handler(client, source, nil, contentType, served, true)
}

func handler(client *http.Client, source *url.URL, headers http.Header, contentType string, served map[string]string, redirect bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// The full forward also works with origins that refuse HEAD, by dropping a GET body.
		method := http.MethodGet
		if redirect {
			if r.Method == http.MethodGet {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Location", source.String())
				w.WriteHeader(http.StatusFound)
				return
			}
			method = http.MethodHead
		}
		req, err := http.NewRequestWithContext(r.Context(), method, source.String(), nil)
		if err != nil {
			http.Error(w, "invalid source", http.StatusInternalServerError)
			return
		}
		req.Header = headers.Clone()
		if req.Header == nil {
			req.Header = http.Header{}
		}
		if ranged := r.Header.Get("Range"); ranged != "" {
			req.Header.Set("Range", ranged)
		}
		resp, err := client.Do(req)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				slog.WarnContext(r.Context(), "forward could not reach the source", "error", err)
			}
			http.Error(w, "source unreachable", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		h := w.Header()
		for k, v := range served {
			h.Set(k, v)
		}
		h.Set("Content-Type", contentType)
		h.Del("Accept-Ranges")
		for _, k := range []string{"Accept-Ranges", "Content-Length", "Content-Range", "Last-Modified", "ETag"} {
			if v := resp.Header.Get(k); v != "" {
				h.Set(k, v)
			}
		}
		status := resp.StatusCode
		if status >= 500 {
			status = http.StatusBadGateway
		}
		w.WriteHeader(status)
		if r.Method == http.MethodHead || status >= 300 {
			return
		}
		if _, err := io.Copy(w, resp.Body); err != nil && r.Context().Err() == nil {
			slog.InfoContext(r.Context(), "forward ended before the range did", "error", err)
		}
	})
}
