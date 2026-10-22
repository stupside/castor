// Package rank measures the links a page offered and orders them for a cast: admitted first, best first.
package rank

import (
	"context"
	"errors"
	"fmt"
	"github.com/stupside/castor/internal/telemetry"
	"log/slog"
	"slices"
	"sync"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// Probes measures one candidate and names the content type it is read as.
type Probes func(context.Context, *source.Stream) (source.Inspection, error)

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
func (r *Ranker) Rank(ctx context.Context, streams []*source.Stream) (rankedStreams []*source.Stream, err error) {
	ctx, finish := telemetry.Start(ctx, telemetry.Rank)
	defer func() { finish(err) }()
	if len(streams) == 0 {
		return nil, errors.New("no streams to rank")
	}
	streams = limitPerHost(ctx, streams)

	pool := make([]*source.Stream, 0, len(streams))
	measured := r.measureAll(ctx, streams)
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	for _, m := range measured {
		v := admit(m)
		if !v.admit {
			logRejection(ctx, m, v)
			continue
		}
		m.LastResort = v.lastResort
		pool = append(pool, m.Stream)
	}
	if len(pool) == 0 {
		return nil, fmt.Errorf("no castable stream: none of %d candidates was admitted", len(streams))
	}

	order := ranked(pool, r.maxHeight)
	best := order[0]
	slog.InfoContext(ctx, "best stream selected", "url", best.URL.String(),
		"bitrate_bps", int64(best.Bitrate()), "height", height(best, r.maxHeight), "last_resort", best.LastResort,
		"alternatives", len(order)-1)
	return order, nil
}

// Measure is the other half of Rank, for the one link an operator named themselves.
func (r *Ranker) Measure(ctx context.Context, stream *source.Stream) (measured *source.Stream, err error) {
	ctx, finish := telemetry.Start(ctx, telemetry.Measure)
	defer func() { finish(err) }()
	one := r.measureAll(ctx, []*source.Stream{stream})[0]
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	// The operator named it, so nothing is ranked against it: only the admission is overruled.
	verdict := admit(one)
	one.LastResort = verdict.lastResort
	slog.InfoContext(ctx, "direct link measured", "url", one.URL.String(),
		"bitrate", int64(one.Bitrate()), "height", height(one.Stream, r.maxHeight),
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
	var wg sync.WaitGroup
	slots := make(chan struct{}, r.cfg.ProbeMaxConcurrency)
	for i, s := range streams {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			slog.DebugContext(ctx, "probing stream", "url", s.URL, "index", i+1, "total", len(streams))
			inspection, err := r.probes(ctx, s)
			info, reach := inspection.Info, inspection.Reach
			// Carried, never re-derived: the ladder was read from a body only the browser held.
			next := *s
			next.Probe, next.Observation, next.LastResort = nil, nil, false
			// Inspection's content type is the one its observation is bound under.
			next.ContentType = inspection.ContentType
			if err != nil {
				slog.WarnContext(ctx, "probe failed", "url", s.URL, "reach", reach, "error", err)
			} else {
				next.Probe = &info
				next.Observation = inspection.Observation
			}
			out[i] = measured{Stream: &next, reach: reach}
		})
	}
	wg.Wait()
	return out
}
