package resolve

import (
	"bufio"
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	bandwidthRe  = regexp.MustCompile(`BANDWIDTH=(\d+)`)
	resolutionRe = regexp.MustCompile(`RESOLUTION=\d+x(\d+)`)
)

// hlsVariant is a single variant stream listed in an HLS master playlist.
type hlsVariant struct {
	URL       *url.URL
	Bandwidth int64
	Height    int // display height from RESOLUTION; 0 when the master omits it
}

// parsePlaylist fetches an HLS playlist and returns its variants. When the
// playlist is a media playlist (no #EXT-X-STREAM-INF tags) it returns a
// single variant pointing at the original URL with zero bandwidth — the
// caller can treat that uniformly.
func parsePlaylist(ctx context.Context, hlsTimeout time.Duration, masterURL *url.URL, headers http.Header) ([]hlsVariant, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, masterURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	maps.Copy(req.Header, headers)

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
		variants      []hlsVariant
		nextIsURL     bool
		currentBW     int64
		currentHeight int
	)
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if nextIsURL {
			nextIsURL = false
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			variantURL, err := masterURL.Parse(line)
			if err != nil {
				continue
			}
			variants = append(variants, hlsVariant{URL: variantURL, Bandwidth: currentBW, Height: currentHeight})
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			// The URL is the next non-comment line. BANDWIDTH is mandatory per
			// the HLS spec and RESOLUTION is optional; take whichever are present.
			currentBW, currentHeight = 0, 0
			if m := bandwidthRe.FindStringSubmatch(line); len(m) == 2 {
				currentBW, _ = strconv.ParseInt(m[1], 10, 64)
			}
			if m := resolutionRe.FindStringSubmatch(line); len(m) == 2 {
				currentHeight, _ = strconv.Atoi(m[1])
			}
			nextIsURL = true
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
