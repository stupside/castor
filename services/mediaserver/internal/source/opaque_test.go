package source

import (
	"net/url"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestDirectFileAudioPreferencesSelectTheTrackAndPreventHandoff(t *testing.T) {
	stream := &Stream{
		URL:         &url.URL{Scheme: "https", Host: "origin.example", Path: "/film.mp4"},
		ContentType: media.MP4,
		Probe: &media.ProbeInfo{
			AudioCodec: media.CodecAAC,
			AudioTracks: []media.AudioTrack{
				{Language: "en", Codec: media.CodecAAC, Channels: 2, Bitrate: 128_000, Default: true},
				{Language: "fra", Codec: media.CodecAC3, Channels: 6, Bitrate: 640_000},
			},
		},
	}
	program, err := ProgramFor(stream)
	if err != nil {
		t.Fatal(err)
	}
	observed := media.Observe(program, *stream.Probe)
	stream.Observation = &observed
	for _, tc := range []struct {
		preferences []string
		index       int
		language    string
		codec       media.Codec
		handoff     bool
	}{
		{[]string{"fr", "en"}, 1, "fra", media.CodecAC3, false},
		{[]string{"de", "en"}, 0, "en", media.CodecAAC, false},
		{[]string{"de"}, 0, "en", media.CodecAAC, true},
		{nil, 0, "en", media.CodecAAC, true},
	} {
		resolver := NewResolver(nil, 1080, tc.preferences, nil)
		got, err := resolver.Resolve(t.Context(), stream, Rendition{})
		if err != nil {
			t.Fatal(err)
		}
		track, ok := got.Program.Track(media.TrackAudio)
		measured, hasMeasurement := got.Program.Measurement()
		heard := stream.Probe.AudioTracks[tc.index]
		if !ok || !hasMeasurement || track.Index != tc.index || got.Rendition.AudioLanguage != tc.language || measured.AudioCodec != tc.codec || measured.AudioBitrate != heard.Bitrate || got.Program.SelfFetchable() != tc.handoff {
			t.Errorf("preferences %v: track=%+v rendition=%+v measurement=%+v handoff=%v", tc.preferences, track, got.Rendition, measured, got.Program.SelfFetchable())
		}
	}
}

func TestDirectFileUsesItsMarkedDefaultAudioTrack(t *testing.T) {
	tracks := []media.AudioTrack{{Language: "en"}, {Language: "fr", Default: true}}
	if index, preferred := selectAudioTrack(tracks, nil); index != 1 || preferred {
		t.Fatalf("default selection = %d, %v, want second track", index, preferred)
	}
}
