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
)

var bandwidthRe = regexp.MustCompile(`BANDWIDTH=(\d+)`)

// hlsVariant holds a single variant stream from an HLS master playlist.
type hlsVariant struct {
	URL       *url.URL
	Bandwidth int64
}

// resolveAllVariants fetches an HLS master playlist and returns all variant
// streams with their bandwidth. If the playlist has no #EXT-X-STREAM-INF tags
// (i.e. it is already a media playlist), a single entry with the original URL
// and bandwidth 0 is returned.
func resolveAllVariants(ctx context.Context, hlsTimeout time.Duration, masterURL *url.URL, headers map[string]string) ([]hlsVariant, error) {
	client := &http.Client{Timeout: hlsTimeout}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, masterURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	for k, v := range headers {
		if strings.HasPrefix(k, ":") {
			continue
		}
		req.Header.Set(k, v)
	}

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
			m := bandwidthRe.FindStringSubmatch(line)
			if len(m) == 2 {
				bw, err := strconv.ParseInt(m[1], 10, 64)
				if err == nil {
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
		return []hlsVariant{{URL: masterURL, Bandwidth: 0}}, nil
	}

	return variants, nil
}
