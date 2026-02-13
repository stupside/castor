package resolve

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/stupside/castor/internal/app"
	"github.com/stupside/castor/internal/media"
)

// Resolve determines the final URL and content type for a stream.
// The stream's Headers are propagated to HLS resolution and ffprobe calls.
func Resolve(ctx context.Context, cfg app.ResolverConfig, stream *media.Stream) (*media.Stream, error) {
	streamURL := stream.URL
	headers := stream.Headers

	ct := stream.ContentType
	if ct == "" {
		info, err := probeStream(ctx, cfg.FFprobePath, cfg.ProbeTimeout, streamURL, headers)
		if err != nil {
			return nil, fmt.Errorf("probing stream: %w", err)
		}
		ct = info.ContentType
	}

	if ct == media.HLS {
		streamURL = resolveHLSVariant(ctx, cfg, streamURL, headers)
	}

	return &media.Stream{URL: streamURL, Headers: headers, ContentType: ct}, nil
}

// RankStreams probes multiple streams concurrently and returns the one with the
// highest bandwidth. Failed probes are logged and skipped. Returns an error
// only if ALL streams fail probing.
func RankStreams(ctx context.Context, cfg app.ResolverConfig, streams []*media.Stream) (*media.Stream, error) {
	var g errgroup.Group
	g.SetLimit(cfg.ProbeMaxConcurrency)

	probed := make([]*media.Stream, len(streams))

	for i, s := range streams {
		g.Go(func() error {
			info, err := probeStream(ctx, cfg.FFprobePath, cfg.ProbeTimeout, s.URL, s.Headers)
			if err != nil {
				return fmt.Errorf("probing stream %q: %w", s.URL, err)
			}
			probed[i] = &media.Stream{
				URL:         s.URL,
				Headers:     s.Headers,
				ContentType: s.ContentType,
				Bandwidth:   max(info.BitRate, 1),
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		slog.Warn("some streams failed probing", "error", err)
	}

	var best *media.Stream
	for _, s := range probed {
		if s != nil && (best == nil || s.Bandwidth > best.Bandwidth) {
			best = s
		}
	}

	if best == nil {
		return nil, fmt.Errorf("all %d streams failed probing", len(streams))
	}

	return best, nil
}

// StreamDetail holds a stream URL and its probed bit rate.
type StreamDetail struct {
	URL     string
	BitRate int64
}

// ListStreams expands HLS variants and probes each stream, returning details
// for display. Failures are logged and skipped.
func ListStreams(ctx context.Context, cfg app.ResolverConfig, streams []*media.Stream) []StreamDetail {
	var details []StreamDetail
	for _, s := range streams {
		if s.ContentType == media.HLS {
			variants, err := resolveAllVariants(ctx, cfg.HLSTimeout, s.URL, s.Headers)
			if err != nil {
				slog.Warn("hls variant resolution failed", "url", s.URL, "error", err)
				continue
			}
			for _, v := range variants {
				info, err := probeStream(ctx, cfg.FFprobePath, cfg.ProbeTimeout, v.URL, s.Headers)
				if err != nil {
					slog.Warn("probe failed", "url", v.URL, "error", err)
					continue
				}
				details = append(details, StreamDetail{URL: v.URL.String(), BitRate: info.BitRate})
			}
		} else {
			info, err := probeStream(ctx, cfg.FFprobePath, cfg.ProbeTimeout, s.URL, s.Headers)
			if err != nil {
				slog.Warn("probe failed", "url", s.URL, "error", err)
				continue
			}
			details = append(details, StreamDetail{URL: s.URL.String(), BitRate: info.BitRate})
		}
	}
	return details
}

func resolveHLSVariant(ctx context.Context, cfg app.ResolverConfig, u *url.URL, headers map[string]string) *url.URL {
	variants, err := resolveAllVariants(ctx, cfg.HLSTimeout, u, headers)
	if err != nil {
		slog.Warn("hls: variant resolution failed, using original", "error", err)
		return u
	}
	best := slices.MaxFunc(variants, func(a, b hlsVariant) int {
		return cmp.Compare(a.Bandwidth, b.Bandwidth)
	})
	return best.URL
}
