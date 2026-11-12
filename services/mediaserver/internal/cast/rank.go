package cast

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/admission"
)

// Rank readies a source's streams as a cast asked the same would, casting nothing.
func (s *Service) Rank(ctx context.Context, req *mediav1.RankRequest) (*mediav1.RankResponse, error) {
	ctx, leave, err := s.resolutions.Enter(ctx)
	switch {
	case errors.Is(err, admission.ErrFull):
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("concurrent resolutions capacity exhausted"))
	case err != nil:
		return nil, connect.NewError(connect.CodeUnavailable, errShutdown)
	}
	defer leave()
	src := originOf(req.GetSource())
	found, err := src.streams()
	if err != nil {
		return nil, s.resolutions.Answer(ctx, connect.NewError(connect.CodeNotFound, err))
	}
	ranked, err := src.ready(ctx, s.caster(req.GetSettings()), found)
	if err != nil {
		return nil, s.resolutions.Answer(ctx, connect.NewError(connect.CodeFailedPrecondition, err))
	}
	out := &mediav1.RankResponse{Ranked: make([]*castorv1.RankedStream, len(ranked))}
	for i, r := range ranked {
		out.Ranked[i] = &castorv1.RankedStream{Url: r.URL.String(), LastResort: r.LastResort}
		if bitrate := r.Bitrate(); bitrate > 0 {
			out.Ranked[i].Bitrate = new(uint64(bitrate))
		}
	}
	return out, nil
}
