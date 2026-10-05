// Package rank measures the links a page offered and orders them for a cast: admitted first, best first.
package rank

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// Probes measures one candidate.
type Probes func(*source.Stream) media.Prober

type Ranker struct {
	cfg Config
	// maxHeight is the tallest picture the cast asked for.
	maxHeight media.HeightCap
	probes    Probes
}

// New binds ranking for one cast, capped at maxHeight, to the measurement it pays in.
func New(cfg Config, maxHeight media.HeightCap, probes Probes) *Ranker {
	return &Ranker{cfg: cfg, maxHeight: maxHeight, probes: probes}
}

// Rank measures every candidate and returns the order a cast walks them in, best first.
func (r *Ranker) Rank(ctx context.Context, streams []*source.Stream) ([]*source.Stream, error) {
	slog.InfoContext(ctx, "ranking streams", "count", len(streams))
	if len(streams) == 0 {
		return nil, errors.New("no streams to rank")
	}
	streams = limitPerHost(ctx, streams)

	pool := make([]*source.Stream, 0, len(streams))
	rejected := make(map[reason]int)
	reasons := make(map[*source.Stream]reason, len(streams))
	measured := r.measureAll(ctx, streams)
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	for _, m := range measured {
		v := admit(m)
		if !v.admit {
			rejected[v.reason]++
			logRejection(ctx, m, v)
			continue
		}
		m.LastResort, reasons[m.Stream] = v.lastResort, v.reason
		pool = append(pool, m.Stream)
	}
	if len(pool) == 0 {
		return nil, fmt.Errorf("no castable stream: none of %d candidates was admitted (%s)", len(streams), tally(rejected))
	}
	if len(rejected) > 0 {
		slog.InfoContext(ctx, "rejected candidates", "kept", len(pool), "detail", tally(rejected))
	}

	order := ranked(pool, r.maxHeight)
	best := order[0]
	slog.InfoContext(ctx, "best stream selected", "url", best.URL.String(),
		"bitrate", int64(best.Bitrate()), "height", height(best), "last_resort", best.LastResort,
		"reason", string(reasons[best]),
		"renditions", best.Ladder, "alternatives", len(order)-1)
	return order, nil
}

// Measure is the other half of Rank, for the one link an operator named themselves.
func (r *Ranker) Measure(ctx context.Context, stream *source.Stream) (*source.Stream, error) {
	one := r.measureAll(ctx, []*source.Stream{stream})[0]
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	// The operator named it, so nothing is ranked against it: only the admission is overruled.
	verdict := admit(one)
	one.LastResort = verdict.lastResort
	slog.InfoContext(ctx, "direct link measured", "url", one.URL.String(),
		"bitrate", int64(one.Bitrate()), "height", height(one.Stream),
		"reason", string(verdict.reason), "last_resort", one.LastResort)
	return one.Stream, nil
}

const maxProbePerHost = 5

// limitPerHost measures at most maxProbePerHost links per host, the ones whose documents promise a ladder first.
func limitPerHost(ctx context.Context, streams []*source.Stream) []*source.Stream {
	seen := make(map[string]int, len(streams))
	kept := make([]*source.Stream, 0, len(streams))
	dropped := 0
	for _, s := range slices.SortedStableFunc(slices.Values(streams), byLadderEvidence) {
		host := s.URL.Hostname()
		if seen[host] >= maxProbePerHost {
			dropped++
			continue
		}
		seen[host]++
		kept = append(kept, s)
	}
	if dropped > 0 {
		slog.InfoContext(ctx, "skipped candidates beyond the per-host measurement cap",
			"dropped", dropped, "kept", len(kept), "per_host_cap", maxProbePerHost)
	}
	return kept
}

// measured is a candidate as its measurement left it, with how far the origin let that measurement get.
type measured struct {
	*source.Stream
	reach media.Reach
}

// measureAll measures every candidate concurrently, bounded by the configured fan-out.
func (r *Ranker) measureAll(ctx context.Context, streams []*source.Stream) []measured {
	out := make([]measured, len(streams))
	var g errgroup.Group
	g.SetLimit(r.cfg.ProbeMaxConcurrency)
	for i, s := range streams {
		g.Go(func() error {
			slog.DebugContext(ctx, "probing stream", "url", s.URL, "index", i+1, "total", len(streams))
			info, reach, err := r.probes(s).Probe(ctx)
			// Carried, never re-derived: the ladder was read from a body only the browser held.
			next := *s
			next.Probe, next.LastResort = nil, false
			if err != nil {
				slog.WarnContext(ctx, "probe failed", "url", s.URL, "reach", reach, "error", err)
			} else {
				next.Probe = &info
				// A container the URL did not name is one the probe just established.
				next.ContentType = cmp.Or(next.ContentType, info.ContentType)
			}
			out[i] = measured{Stream: &next, reach: reach}
			return nil
		})
	}
	_ = g.Wait()
	return out
}
