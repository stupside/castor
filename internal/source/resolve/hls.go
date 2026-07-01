package resolve

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/stupside/castor/internal/media"
)

var bandwidthRe = regexp.MustCompile(`BANDWIDTH=(\d+)`)

// hlsVariant is a single variant stream listed in an HLS master playlist.
type hlsVariant struct {
	URL       *url.URL
	Bandwidth int64
}

// parsePlaylist fetches an HLS playlist and returns its variants. When the
// playlist is a media playlist (no #EXT-X-STREAM-INF tags) it returns a
// single variant pointing at the original URL with zero bandwidth — the
// caller can treat that uniformly.
func parsePlaylist(ctx context.Context, hlsTimeout time.Duration, masterURL *url.URL, headers map[string]string) ([]hlsVariant, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, masterURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	media.ApplyHTTPHeaders(req, headers)

	client := &http.Client{Timeout: hlsTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching playlist: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetching playlist: HTTP %d", resp.StatusCode)
	}

	var (
		variants  []hlsVariant
		nextIsBW  bool
		currentBW int64
	)
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if nextIsBW {
			nextIsBW = false
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			variantURL, err := masterURL.Parse(line)
			if err != nil {
				continue
			}
			variants = append(variants, hlsVariant{URL: variantURL, Bandwidth: currentBW})
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			if m := bandwidthRe.FindStringSubmatch(line); len(m) == 2 {
				if bw, err := strconv.ParseInt(m[1], 10, 64); err == nil {
					currentBW = bw
					nextIsBW = true
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading playlist: %w", err)
	}

	if len(variants) == 0 {
		variants = []hlsVariant{{URL: masterURL, Bandwidth: 0}}
	}
	return variants, nil
}
