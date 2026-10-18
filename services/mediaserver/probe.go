package mediaserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/probe"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/follow"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// candidateProbe composes source resolution and inspection for ranking. The
// republisher scopes the temporary reading and joins it before returning.
type candidateProbe struct {
	stream    *source.Stream
	client    source.Client
	resolver  *source.Resolver
	timelines follow.Republisher
	binary    probe.FFprobe
}

func (p candidateProbe) Probe(ctx context.Context) (info media.ProbeInfo, reach media.Reach, err error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	stream := *p.stream
	if stream.ContentType == "" {
		stream.ContentType = formats.Identify(ctx, p.client, stream.URL, stream.Headers)
	}
	if !media.IsSegmented(stream.ContentType) {
		return p.binary.Link(probe.Link{URL: stream.URL, Headers: stream.Headers, InputArgs: formats.InputArgs(stream.ContentType, 0)}, fetchTimeout).Probe(ctx)
	}
	defer func() {
		if failure, ok := errors.AsType[*timeline.Failure](err); ok {
			switch failure.Status {
			case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
				reach = media.ReachRefused
			}
		}
	}()
	resolved, err := p.resolver.Resolve(ctx, &stream, source.Rendition{})
	if err != nil {
		return info, media.ReachUnproven, err
	}
	info, reach, err = p.timelines.Probe(ctx, resolved.Program, func(program media.Program) media.Prober {
		inputs := make([]probe.Input, 0, len(program.Inputs))
		for _, in := range program.Inputs {
			args := append(ffmpeg.ProtocolArgs(), ffmpeg.HeaderArgs(in.Headers)...)
			args = append(args, formats.InputArgs(in.ContentType, 0)...)
			inputs = append(inputs, probe.Input{ID: in.ID, URL: in.URL.String(), Args: args})
		}
		return p.binary.Source(program, inputs)
	})
	// Keep the origin format: the local reading is HLS even for DASH.
	info.ContentType = stream.ContentType
	// This measurement describes the selected rendition, not the master's program IDs.
	info.ProgramHeights = nil
	return info, reach, err
}
