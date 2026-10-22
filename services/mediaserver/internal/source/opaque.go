package source

import (
	"context"
	"fmt"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// opaque reads a source whole: the reader of what no format claims.
type opaque struct{}

// Resolve reads the whole source behind one URL.
func (opaque) Resolve(_ context.Context, env Env, s Subject) (Resolution, error) {
	program, err := ProgramFor(&s.Stream)
	if err != nil {
		return Resolution{Origin: s.Origin}, fmt.Errorf("normalizing source program: %w", err)
	}
	// Liveness and framing come from the origin, not the link.
	for i := range program.Inputs {
		program.Inputs[i].Fetch.Live = s.Origin.Live
		program.Inputs[i].Fetch.Framing = s.Origin.Framing
	}
	var chosen Rendition
	if probe, observed := program.Measurement(); observed && len(probe.AudioTracks) > 0 {
		index, preferred := selectAudioTrack(probe.AudioTracks, env.AudioLanguages)
		for i := range program.Tracks {
			if program.Tracks[i].Kind == media.TrackAudio {
				program.Tracks[i].Index = index
			}
		}
		program.NeedsTrackSelection = preferred && len(probe.AudioTracks) > 1
		track := probe.AudioTracks[index]
		chosen.AudioLanguage = track.Language
		measurement := probe.Clone()
		measurement.AudioCodec, measurement.AudioChannels, measurement.AudioSampleRate = track.Codec, track.Channels, track.SampleRate
		program.SetMeasurement(measurement)
	}
	return Resolution{Program: program, Origin: s.Origin, Rendition: chosen}, nil
}

// selectAudioTrack applies language order before the container's default mark.
func selectAudioTrack(tracks []media.AudioTrack, languages []string) (index int, preferred bool) {
	for _, language := range languages {
		for i, track := range tracks {
			if SameLanguage(track.Language, language) {
				return i, true
			}
		}
	}
	for i, track := range tracks {
		if track.Default {
			return i, false
		}
	}
	return 0, false
}

func (opaque) Timeline(Client, media.Input, media.TrackKind) timeline.Reading {
	return timeline.Reading{}
}

func (opaque) InputArgs(int) []string { return nil }

// containers are the files opaque reads whole, by the names a link announces them under.
var containers = []Identity{
	{ContentType: media.MP4, Extensions: []string{".mp4"}, MIMETypes: []string{media.MP4}},
	{ContentType: media.MKV, Extensions: []string{".mkv"}, MIMETypes: []string{media.MKV}},
	{ContentType: media.WebM, Extensions: []string{".webm"}, MIMETypes: []string{media.WebM}},
	{ContentType: media.AVI, Extensions: []string{".avi"}, MIMETypes: []string{media.AVI}},
	{ContentType: media.MOV, Extensions: []string{".mov"}, MIMETypes: []string{media.MOV}},
	{ContentType: media.MPEGTS, MIMETypes: []string{media.MPEGTS}},
	{ContentType: media.FLV, MIMETypes: []string{media.FLV}},
}
