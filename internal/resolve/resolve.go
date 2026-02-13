package resolve

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/stupside/castor/internal/media"
)

// Resolve determines the final URL and content type for a given URL.
func Resolve(ctx context.Context, ffprobePath string, itemURL *url.URL) (*media.Stream, error) {
	if ct := media.DetectFromExtension(itemURL); ct != "" {
		streamURL := itemURL
		if ct == media.HLS {
			resolved, err := ResolveBestVariant(ctx, itemURL)
			if err != nil {
				slog.Warn("hls: variant resolution failed, using original", "error", err)
			} else {
				streamURL = resolved
			}
		}
		return &media.Stream{URL: streamURL, ContentType: ct}, nil
	}

	info, err := ProbeStream(ctx, ffprobePath, itemURL)
	if err != nil {
		return nil, fmt.Errorf("probing stream: %w", err)
	}

	streamURL := itemURL
	if info.ContentType == media.HLS {
		resolved, err := ResolveBestVariant(ctx, itemURL)
		if err != nil {
			slog.Warn("hls: variant resolution failed, using original", "error", err)
		} else {
			streamURL = resolved
		}
	}

	return &media.Stream{URL: streamURL, ContentType: info.ContentType}, nil
}
