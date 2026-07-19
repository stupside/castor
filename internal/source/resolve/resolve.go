package resolve

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

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

// minContentDuration is the shortest runtime treated as real content. Pre-roll
// ads and ad-pods run well under it; the shortest real title (a ~11-minute
// episode) sits above. A candidate whose duration is known and shorter is an ad
// and is dropped like any other decoy. Unknown duration (live, no endlist) is
// not treated as short.
const minContentDuration = 5 * time.Minute

// maxProbePerHost caps how many candidates from one host RankStreams probes. An
// embed proxy emits a master plus a long tail of variant playlists behind one
// signature; probing all of them trips the host's rate limiter (HTTP 429),
// which poisons the ranking and kills the pull. Candidates arrive master-first,
// so the first few per host keep the master and drop the redundant tail.
const maxProbePerHost = 5

// limitPerHost keeps at most maxProbePerHost candidates per host, in order, so
// the probe stage can't fire a dozen redundant variant requests at one proxy
// and trip its rate limiter.
func limitPerHost(ctx context.Context, streams []*media.Stream) []*media.Stream {
	seen := make(map[string]int, len(streams))
	kept := make([]*media.Stream, 0, len(streams))
	dropped := 0
	for _, s := range streams {
		host := s.URL.Hostname()
		if seen[host] >= maxProbePerHost {
			dropped++
			continue
		}
		seen[host]++
		kept = append(kept, s)
	}
	if dropped > 0 {
		slog.InfoContext(ctx, "skipped redundant variant candidates to avoid rate limiting",
			"dropped", dropped, "kept", len(kept), "per_host_cap", maxProbePerHost)
	}
	return kept
}

// RankStreams probes every candidate in parallel and returns the highest-
// bandwidth playable one. A stream that probes cleanly but carries no castable
// video+audio, or is too short to be anything but a spliced-in ad, is dropped
// hard so it can't win when the real sources are unreachable. A stream whose
// probe fails (403/timeout/reset) is kept at zero bandwidth as a last resort,
// since the puller reconnects differently and may still succeed. If every
// candidate is a decoy, ranking fails cleanly.
func RankStreams(ctx context.Context, cfg Config, streams []*media.Stream) (*media.Stream, error) {
	slog.InfoContext(ctx, "ranking streams", "count", len(streams))
	if len(streams) == 0 {
		return nil, fmt.Errorf("no streams to rank")
	}
	streams = limitPerHost(ctx, streams)

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
			case info.Duration > 0 && info.Duration < minContentDuration:
				// Too short to be a feature/episode → spliced-in ad, drop hard so it
				// can't win over the real title on bandwidth.
				slog.WarnContext(ctx, "stream rejected: too short to be feature content, treating as ad",
					"url", s.URL, "duration", info.Duration)
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
		return nil, fmt.Errorf("no castable stream: all %d candidates were unreachable, carried no video+audio, or were ads", len(streams))
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
