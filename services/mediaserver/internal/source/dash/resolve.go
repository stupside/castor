package dash

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// Resolve reads the presentation, chooses the representation the ceiling admits and the sound to play with it.
func (Format) Resolve(ctx context.Context, env source.Env, s source.Subject) (source.Resolution, error) {
	stream, origin := s.Stream, s.Origin
	body, _, status, err := env.Client.Fetch(ctx, stream.URL, stream.Headers)
	if err != nil {
		return source.Resolution{Origin: origin}, fmt.Errorf("reading the DASH presentation (status %d): %w", status, err)
	}
	m, ok := parse(body)
	if !ok {
		return source.Resolution{Origin: origin}, errors.New("the document is not a DASH presentation castor can read")
	}
	periods := m.placed()
	film := feature(periods, m.live())

	origin.Live, origin.Segmented, origin.Framing = m.live(), true, media.FramingOutOfBand
	// Periods are pieces encoded apart, whose parameters and timestamps restart at each boundary.
	origin.Spliced = len(periods) > 1
	if total := periods[len(periods)-1].start + periods[len(periods)-1].duration; !origin.Live && total > 0 {
		origin.Duration = total
	}
	origin.Protection = protection(periods)
	var heard *offered
	if audio := offers(film.Period, media.TrackAudio); len(audio) > 0 {
		best := preferredAudio(audio, env.AudioLanguages)
		heard = &best
	}
	origin.Renditions = ladder(film, stream.Probe, heard)

	var picture, sound *offered
	var chosen source.Rendition
	if len(origin.Renditions) > 0 {
		chosen = env.Choose(ctx, origin, s, byHeight)
		video := offers(film.Period, media.TrackVideo)
		picture = &video[slices.IndexFunc(video, func(o offered) bool { return o.rep.Id == chosen.Representation })]
	}
	if picture == nil || !picture.muxed() {
		sound = heard
	}
	if sound != nil {
		chosen.AudioLanguage = sound.set.Lang
	} else if picture != nil {
		chosen.AudioLanguage = picture.set.Lang
	}
	if picture == nil && sound == nil {
		return source.Resolution{Origin: origin}, errors.New("the DASH presentation offers neither picture nor sound castor can cast")
	}

	fetch := media.Fetch{Segmented: true, Framing: media.FramingOutOfBand, Live: origin.Live, Spliced: origin.Spliced}
	headers := env.Client.Replay(stream.URL, stream.Headers)
	input := func(id media.InputID, o *offered) media.Input {
		return media.Input{ID: id, URL: stream.URL, Representation: o.rep.Id, Headers: headers, ContentType: media.DASH, Fetch: fetch}
	}
	var inputs []media.Input
	var tracks []media.TrackRef
	end := media.EndAtLongest
	switch {
	case picture != nil && sound != nil:
		inputs = []media.Input{input(media.PrimaryInputID, picture), input(media.AudioInputID, sound)}
		tracks = []media.TrackRef{{Input: media.PrimaryInputID, Kind: media.TrackVideo}, {Input: media.AudioInputID, Kind: media.TrackAudio}}
		// Sound missing from a Period, or from one a live presentation has yet to publish, would cut the film short at the shortest input.
		if w, _ := wantedIn(periods, film, media.TrackAudio, sound.rep.Id); !origin.Live && !slices.ContainsFunc(periods, func(p placed) bool {
			_, has := w.in(p.Period)
			return !has
		}) {
			end = media.EndAtShortest
		}
	case picture != nil:
		inputs = []media.Input{input(media.PrimaryInputID, picture)}
		tracks = []media.TrackRef{{Input: media.PrimaryInputID, Kind: media.TrackVideo}, {Input: media.PrimaryInputID, Kind: media.TrackAudio, Optional: true}}
	default:
		inputs = []media.Input{input(media.PrimaryInputID, sound)}
		tracks = []media.TrackRef{{Input: media.PrimaryInputID, Kind: media.TrackAudio}}
	}
	if sound != nil {
		slog.InfoContext(ctx, "the presentation publishes sound apart; both representations will be read",
			"video", chosen.Representation, "audio", sound.rep.Id, "periods", len(periods))
	}

	program, err := source.Narrowed(media.Program{Inputs: inputs, Tracks: tracks, ClockInput: media.PrimaryInputID, EndPolicy: end}, chosen, &stream)
	if err != nil {
		return source.Resolution{Origin: origin, Rendition: chosen}, fmt.Errorf("binding the chosen representation: %w", err)
	}
	return source.Resolution{Program: program, Origin: origin, Rendition: chosen}, nil
}
