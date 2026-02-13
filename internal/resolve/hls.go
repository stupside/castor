package resolve

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var hlsClient = &http.Client{Timeout: 30 * time.Second}

var bandwidthRe = regexp.MustCompile(`BANDWIDTH=(\d+)`)

// ResolveBestVariant fetches an HLS master playlist and returns the variant URL
// with the highest BANDWIDTH. If the playlist has no variants (i.e. it is
// already a media playlist), the original URL is returned unchanged.
func ResolveBestVariant(ctx context.Context, masterURL *url.URL) (*url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, masterURL.String(), nil)
	if err != nil {
		return masterURL, fmt.Errorf("creating request: %w", err)
	}

	resp, err := hlsClient.Do(req)
	if err != nil {
		return masterURL, fmt.Errorf("fetching playlist: %w", err)
	}
	defer resp.Body.Close()

	var (
		bestBW  int64
		bestURI string
		nextIsBW bool
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
			if currentBW > bestBW {
				bestBW = currentBW
				bestURI = line
			}
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
		return masterURL, fmt.Errorf("reading playlist: %w", err)
	}

	if bestURI == "" {
		slog.Debug("hls: no variants found, using original URL")
		return masterURL, nil
	}

	variantURL, err := masterURL.Parse(bestURI)
	if err != nil {
		return masterURL, fmt.Errorf("resolving variant URI %q: %w", bestURI, err)
	}

	slog.Debug("hls: selected best variant", "bandwidth", bestBW, "url", variantURL.String())
	return variantURL, nil
}
