package resolve

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// hlsVariant is a single variant stream listed in an HLS master playlist.
type hlsVariant struct {
	URL       *url.URL
	Bandwidth int64
	Height    int // display height from RESOLUTION; 0 when the master omits it
}

// fetchPlaylist fetches an HLS playlist and returns its body.
func fetchPlaylist(ctx context.Context, hlsTimeout time.Duration, url *url.URL, headers http.Header) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url.String(), nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	maps.Copy(req.Header, headers)

	client := &http.Client{Timeout: hlsTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching playlist: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetching playlist: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading playlist: %w", err)
	}
	return string(body), nil
}

// parsePlaylist parses an HLS playlist body and returns its variants.
// For a media playlist (no #EXT-X-STREAM-INF) it returns a single variant
// with the base URL and zero bandwidth — the caller can treat that uniformly.
//
// The live result reports whether the document is a media playlist with no
// #EXT-X-ENDLIST, i.e. a live edge. It is always false for a master
// playlist: the master carries no endlist signal of its own, so callers
// must not read false as "VOD" in that case.
func parsePlaylist(body string, baseURL *url.URL) ([]hlsVariant, bool) {
	var (
		endlist       bool
		variants      []hlsVariant
		nextIsURL     bool
		currentBW     int64
		currentHeight int
	)
	for line := range strings.Lines(body) {
		line = strings.TrimRight(line, "\n\r")
		if line == "" {
			continue
		}

		if nextIsURL {
			nextIsURL = false
			if strings.HasPrefix(line, "#") {
				continue
			}
			variantURL, err := baseURL.Parse(line)
			if err != nil {
				continue
			}
			variants = append(variants, hlsVariant{URL: variantURL, Bandwidth: currentBW, Height: currentHeight})
			continue
		}

		if line == "#EXT-X-ENDLIST" {
			endlist = true
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			currentBW, currentHeight = 0, 0
			if _, attrs, ok := strings.Cut(line, ":"); ok {
				for attr := range strings.SplitSeq(attrs, ",") {
					k, v, ok := strings.Cut(attr, "=")
					if !ok {
						continue
					}
					switch k {
					case "BANDWIDTH":
						currentBW, _ = strconv.ParseInt(v, 10, 64)
					case "RESOLUTION":
						if _, h, ok := strings.Cut(v, "x"); ok {
							currentHeight, _ = strconv.Atoi(h)
						}
					}
				}
			}
			nextIsURL = true
		}
	}

	if len(variants) == 0 {
		return []hlsVariant{{URL: baseURL, Bandwidth: 0}}, !endlist
	}
	return variants, false
}
