// Package source turns a link into the program a cast reads: what the origin publishes, and the formats that read it.
package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Resolution is what resolving one link established.
type Resolution struct {
	// Program is what castor reads.
	Program media.Program

	// Origin is what the source offered, whatever was chosen from it.
	Origin Origin

	// Rendition is the rung chosen, zero when the source published no ladder.
	Rendition Rendition
}

// Resolver resolves links through the formats it was bound to.
type Resolver struct {
	env     Env
	formats Formats
}

// NewResolver tries audio languages in order, then uses the source's default.
func NewResolver(client Client, maxHeight media.HeightCap, audioLanguages []string, formats Formats) *Resolver {
	return &Resolver{
		env:     Env{Client: client, MaxHeight: maxHeight, AudioLanguages: audioLanguages},
		formats: formats,
	}
}

// Resolve reads a link, narrowed to chosen and to rungs within ceiling, each zero for none.
func (r *Resolver) Resolve(ctx context.Context, stream *Stream, chosen Rendition, ceiling media.Bitrate) (Resolution, error) {
	if stream == nil || stream.URL == nil {
		return Resolution{}, errors.New("source has no URL")
	}
	origin := Origin{}
	if probe := stream.Probe; probe != nil {
		origin.Duration = probe.Duration
		origin.Live = probe.Duration == 0
	}
	origin.Segmented = media.IsSegmented(stream.ContentType)
	resolved, err := r.formats.Claiming(stream.ContentType).Resolve(ctx, r.env, Subject{Stream: *stream, Origin: origin, Chosen: chosen, Ceiling: ceiling})
	if err != nil {
		return resolved, err
	}
	if drm := resolved.Origin.Protection; drm != "" {
		return resolved, fmt.Errorf("the source is protected by DRM (%s), which castor cannot decrypt", drm)
	}
	if selected := resolved.Rendition.AudioLanguage; selected != "" && len(r.env.AudioLanguages) > 0 {
		matched := false
		for _, preferred := range r.env.AudioLanguages {
			if SameLanguage(selected, preferred) {
				matched = true
				break
			}
		}
		if !matched {
			slog.WarnContext(ctx, "the selected audio is not in the preferred languages", "selected", selected, "languages", r.env.AudioLanguages)
		}
	}
	slog.InfoContext(ctx, "source resolved", "content_type", stream.ContentType)
	return resolved, nil
}

// ProgramFor is the single-input program one stream names, before any format reads it.
func ProgramFor(stream *Stream) (media.Program, error) {
	program, err := media.NewProgram(media.Program{
		Inputs: []media.Input{{
			ID: media.PrimaryInputID, URL: stream.URL, Headers: stream.Headers,
			ContentType: stream.ContentType, Fetch: media.Fetch{Segmented: media.IsSegmented(stream.ContentType)},
		}},
		Tracks: []media.TrackRef{
			{Input: media.PrimaryInputID, Kind: media.TrackVideo, Optional: true},
			{Input: media.PrimaryInputID, Kind: media.TrackAudio, Optional: true},
		},
		ClockInput: media.PrimaryInputID,
		EndPolicy:  media.EndAtLongest,
	})
	if err != nil {
		return media.Program{}, err
	}
	if stream.Observation != nil {
		program.SetObservation(*stream.Observation)
	}
	return program, nil
}

// Narrowed keeps the selected rung's declaration apart from an observation of
// the exact inputs and tracks this resolution will read.
func Narrowed(program media.Program, chosen Rendition, stream *Stream) (media.Program, error) {
	narrowed, err := media.NewProgram(program)
	if err != nil {
		return media.Program{}, err
	}
	if chosen.Declared != nil {
		narrowed.SetDeclaration(*chosen.Declared)
	}
	if stream.Observation != nil {
		narrowed.SetObservation(*stream.Observation)
	}
	return narrowed, nil
}
