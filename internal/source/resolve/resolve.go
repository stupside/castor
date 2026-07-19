package resolve

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/stupside/castor/internal/media"
)

// Resolve determines the final URL and content type for a stream. When the
// source is an HLS playlist with multiple variants, it picks the highest-
// bandwidth one. Stream headers are preserved through resolution.
func Resolve(ctx context.Context, cfg Config, stream *media.Stream) (*media.Stream, error) {
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
		variants, err := parsePlaylist(ctx, cfg.HLSTimeout, streamURL, headers)
		if err != nil {
			slog.WarnContext(ctx, "HLS playlist resolution failed, using original", "error", err)
		} else {
			best := slices.MaxFunc(variants, func(a, b hlsVariant) int {
				return cmp.Compare(a.Bandwidth, b.Bandwidth)
			})
			streamURL = best.URL
		}
	}

	return &media.Stream{
		URL:         streamURL,
		Headers:     headers,
		ContentType: ct,
	}, nil
}

// RankStreams probes the candidate streams in parallel and returns the highest-
// bandwidth playable one. A single candidate is returned as-is (nothing to
// rank). Otherwise: a stream that probes cleanly but carries no castable
// video+audio is a decoy and is dropped hard, so it can never win by default
// when the real sources are unreachable; a stream whose probe *fails*
// (403/timeout/reset) is kept at zero bandwidth as a last-resort fallback, since
// the puller reconnects differently and may still succeed. If every candidate
// is a decoy, ranking fails cleanly instead of handing the puller a stream it
// cannot map.
func RankStreams(ctx context.Context, cfg Config, streams []*media.Stream) (*media.Stream, error) {
	slog.InfoContext(ctx, "ranking streams", "count", len(streams))
	switch len(streams) {
	case 0:
		return nil, fmt.Errorf("no streams to rank")
	case 1:
		return streams[0], nil
	}

	type candidate struct {
		stream *media.Stream
		decoy  bool
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.ProbeMaxConcurrency)
	cands := make([]candidate, len(streams))

	for i, s := range streams {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			slog.DebugContext(ctx, "probing stream", "url", s.URL, "index", i+1, "total", len(streams))
			info, err := probeStream(ctx, cfg.FFprobePath, cfg.ProbeTimeout, s.URL, s.Headers)
			out := &media.Stream{URL: s.URL, Headers: s.Headers, ContentType: s.ContentType}
			switch {
			case err != nil:
				// Transient failure (403/timeout/reset): keep as a zero-bandwidth fallback.
				slog.WarnContext(ctx, "probe failed", "url", s.URL, "error", err)
			case !info.Playable():
				// Probed cleanly but no castable video+audio → decoy, drop hard.
				slog.WarnContext(ctx, "stream rejected: no castable video+audio",
					"url", s.URL, "has_video", info.HasVideo, "has_audio", info.HasAudio)
				cands[i] = candidate{stream: out, decoy: true}
				return
			default:
				out.Bandwidth = max(info.BitRate, 1)
				slog.DebugContext(ctx, "probed stream", "url", s.URL, "bitrate", info.BitRate)
			}
			cands[i] = candidate{stream: out}
		})
	}
	wg.Wait()

	pool := make([]*media.Stream, 0, len(cands))
	decoys := 0
	for _, c := range cands {
		if c.decoy {
			decoys++
			continue
		}
		pool = append(pool, c.stream)
	}
	if len(pool) == 0 {
		return nil, fmt.Errorf("no castable stream: all %d candidates were unreachable or carried no video+audio", len(streams))
	}

	best := slices.MaxFunc(pool, func(a, b *media.Stream) int {
		return cmp.Compare(a.Bandwidth, b.Bandwidth)
	})
	if decoys > 0 {
		slog.InfoContext(ctx, "rejected decoy streams", "count", decoys, "kept", len(pool))
	}
	slog.InfoContext(ctx, "best stream selected", "url", best.URL.String(), "bitrate", best.Bandwidth)
	return best, nil
}

// StreamDetail holds a stream URL and its probed bit rate, for display.
type StreamDetail struct {
	URL     string
	BitRate int64
}

// ListStreams expands HLS variants and probes each, returning details for
// display. Failures are logged and skipped.
func ListStreams(ctx context.Context, cfg Config, streams []*media.Stream) []StreamDetail {
	var details []StreamDetail
	for _, s := range streams {
		if s.ContentType == media.HLS {
			variants, err := parsePlaylist(ctx, cfg.HLSTimeout, s.URL, s.Headers)
			if err != nil {
				slog.WarnContext(ctx, "HLS variant resolution failed", "url", s.URL, "error", err)
				continue
			}
			for _, v := range variants {
				info, err := probeStream(ctx, cfg.FFprobePath, cfg.ProbeTimeout, v.URL, s.Headers)
				if err != nil {
					slog.WarnContext(ctx, "probe failed", "url", v.URL, "error", err)
					continue
				}
				details = append(details, StreamDetail{URL: v.URL.String(), BitRate: info.BitRate})
			}
			continue
		}
		info, err := probeStream(ctx, cfg.FFprobePath, cfg.ProbeTimeout, s.URL, s.Headers)
		if err != nil {
			slog.WarnContext(ctx, "probe failed", "url", s.URL, "error", err)
			continue
		}
		details = append(details, StreamDetail{URL: s.URL.String(), BitRate: info.BitRate})
	}
	return details
}
