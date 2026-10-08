// Package tmdb is a minimal TMDB v3 API client for browse endpoints only.
package tmdb

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	apiBase        = "https://api.themoviedb.org/3"
	imageBase      = "https://image.tmdb.org/t/p/"
	requestTimeout = 10 * time.Second
)

// Media is a castable kind of title, as TMDB names it in paths and results.
type Media string

const (
	MediaMovie Media = "movie"
	MediaTV    Media = "tv"
)

// Config is the TMDB account the interactive cast browses with.
type Config struct {
	APIKey string `yaml:"api_key"`
}

// Client reads TMDB's catalog with one account.
type Client struct {
	apiKey string
	base   string
	images string
	http   *http.Client
}

// New is a Client of the account cfg names.
func New(cfg Config) *Client {
	// Keep connections warm for TUI's many small calls (search, details, discover).
	// Cloned, so proxy settings and dial and TLS timeouts still apply.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 8
	return &Client{
		apiKey: cfg.APIKey,
		base:   apiBase,
		images: imageBase,
		http:   &http.Client{Timeout: requestTimeout, Transport: transport},
	}
}

// posterSize is the width the browser shows posters at.
const posterSize = "w500"

// Poster streams a poster at posterSize; caller closes it.
func (c *Client) Poster(ctx context.Context, posterPath string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.images+posterSize+posterPath, nil)
	if err != nil {
		return nil, fmt.Errorf("tmdb: build poster request: %w", err)
	}
	return c.open(req, "poster "+posterPath)
}

func (c *Client) get(ctx context.Context, path string, extra url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return fmt.Errorf("tmdb: build request: %w", err)
	}
	// Search hands the same values to two goroutines, so setting a key in place races.
	q := extra.Clone()
	if q == nil {
		q = url.Values{}
	}
	q.Set("api_key", c.apiKey)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/json")

	rc, err := c.open(req, path)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	// Detect gzip by content; TMDB CDN returns gzip without Content-Encoding header.
	br := bufio.NewReader(rc)
	var body io.Reader = br
	if peek, _ := br.Peek(2); bytes.HasPrefix(peek, []byte{0x1f, 0x8b}) {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return fmt.Errorf("tmdb: gunzip %s: %w", path, err)
		}
		defer func() { _ = gz.Close() }()
		body = gz
	}

	if err := json.UnmarshalRead(body, out); err != nil {
		return fmt.Errorf("tmdb: decode %s: %w", path, err)
	}
	return nil
}

// open returns a 2xx body; its errors name what, never the URL, which carries api_key.
func (c *Client) open(req *http.Request, what string) (io.ReadCloser, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return nil, fmt.Errorf("tmdb: %s: %w", what, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("tmdb: %s: status %d", what, resp.StatusCode)
	}
	return resp.Body, nil
}
