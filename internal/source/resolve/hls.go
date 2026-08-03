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

	"github.com/grafov/m3u8"
)

// hlsVariant is a single variant stream listed in an HLS master playlist.
type hlsVariant struct {
	URL       *url.URL
	Bandwidth int64
	Height    int // display height from RESOLUTION; 0 when the master omits it
}

// hlsMaster is a parsed HLS document reduced to what selection needs. A media
// playlist reduces to a single synthetic variant, so callers treat both shapes
// uniformly.
type hlsMaster struct {
	Variants []hlsVariant

	// Live reports that the document is a media playlist with no
	// #EXT-X-ENDLIST, i.e. a live edge. It is always false for a master
	// playlist: the master carries no endlist signal of its own, so callers
	// must not read false as "VOD" in that case.
	Live bool
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

// parsePlaylist decodes an HLS document into the variants it offers, resolving
// every URI against baseURL.
//
// The document is decoded by m3u8, not by us: the tag grammar is a spec surface
// (quoted attribute values carrying the same comma that separates attributes,
// I-frame variants that state their URI inline as an attribute) and reading it
// line by line gets those subtly wrong. Decoding is non-strict so a real-world
// playlist with an unknown tag still parses. What stays here is only the part
// that is castor's policy rather than the format's.
func parsePlaylist(body string, baseURL *url.URL) (hlsMaster, error) {
	playlist, _, err := m3u8.DecodeFrom(strings.NewReader(body), false)
	if err != nil {
		return hlsMaster{}, fmt.Errorf("decoding playlist: %w", err)
	}

	switch p := playlist.(type) {
	case *m3u8.MediaPlaylist:
		// The document is itself the thing to read, and it is the one shape that
		// reports its own liveness: no #EXT-X-ENDLIST means a live edge.
		return hlsMaster{Variants: []hlsVariant{{URL: baseURL}}, Live: !p.Closed}, nil
	case *m3u8.MasterPlaylist:
		return masterFrom(p, baseURL), nil
	default:
		return hlsMaster{}, fmt.Errorf("unsupported playlist type %T", playlist)
	}
}

// masterFrom reduces a decoded master to the variants castor can cast.
func masterFrom(playlist *m3u8.MasterPlaylist, baseURL *url.URL) hlsMaster {
	var out hlsMaster
	for _, variant := range playlist.Variants {
		if variant == nil {
			continue
		}
		// An I-frame playlist is a trick-play track: keyframes only, no audio,
		// never something to cast.
		if variant.Iframe {
			continue
		}
		variantURL, err := baseURL.Parse(variant.URI)
		if err != nil {
			continue
		}
		out.Variants = append(out.Variants, hlsVariant{
			URL:       variantURL,
			Bandwidth: int64(variant.Bandwidth),
			Height:    resolutionHeight(variant.Resolution),
		})
	}
	return out
}

// resolutionHeight reads the height out of a RESOLUTION attribute ("1920x1080"),
// returning 0 when the master omits or malforms it.
func resolutionHeight(resolution string) int {
	_, height, ok := strings.Cut(resolution, "x")
	if !ok {
		return 0
	}
	h, err := strconv.Atoi(height)
	if err != nil {
		return 0
	}
	return h
}
