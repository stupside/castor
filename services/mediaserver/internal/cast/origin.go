package cast

import (
	"context"
	"fmt"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/wire"
)

// origin is a direct stream or already-resolved candidates; no page is opened here.
type origin interface {
	streams() ([]*source.Stream, error)
	// ready is streams in the order the cast walks them.
	ready(ctx context.Context, caster Caster, streams []*source.Stream) ([]*source.Stream, error)
}

func originOf(src *mediav1.Source) origin {
	if streams := src.GetStreams(); streams != nil {
		return resolved{candidates: streams.GetStreams()}
	}
	return stream{src.GetStream()}
}

// resolved is a candidate list produced by scrapingserver; mediaserver only measures and ranks it.
type resolved struct{ candidates []*castorv1.StreamCandidate }

func (r resolved) streams() ([]*source.Stream, error) {
	found := make([]*source.Stream, 0, len(r.candidates))
	for _, s := range r.candidates {
		one, err := wire.FromCandidate(s)
		if err != nil {
			return nil, err
		}
		found = append(found, one)
	}
	return found, nil
}
func (resolved) ready(ctx context.Context, caster Caster, streams []*source.Stream) ([]*source.Stream, error) {
	return caster.Rank(ctx, streams)
}

// stream is measured as is, never ranked.
type stream struct{ *castorv1.Stream }

func (s stream) streams() ([]*source.Stream, error) {
	one, err := wire.FromStream(s.Stream)
	if err != nil {
		return nil, err
	}
	return []*source.Stream{one}, nil
}

func (stream) ready(ctx context.Context, caster Caster, streams []*source.Stream) ([]*source.Stream, error) {
	one, err := caster.Measure(ctx, streams[0])
	if err != nil {
		return nil, fmt.Errorf("measuring stream: %w", err)
	}
	return []*source.Stream{one}, nil
}
