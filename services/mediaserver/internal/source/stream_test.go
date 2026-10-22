package source_test

import (
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/sourcetest"
)

func TestWebMNameDisambiguatesMatroskaProbe(t *testing.T) {
	probed := media.ProbeInfo{ContentType: media.MKV, VideoCodec: media.CodecVP8, AudioCodec: media.CodecVorbis}
	for _, tc := range []struct{ raw, announced, want string }{
		{"https://origin.example/movie.webm", "", media.WebM},
		{"https://origin.example/movie.mkv", "", media.MKV},
		// An announced content type outranks what ffprobe calls the container.
		{"https://origin.example/movie", media.WebM, media.WebM},
	} {
		s := source.Stream{URL: sourcetest.URL(t, tc.raw), ContentType: tc.announced}
		if got := s.MeasuredContentType(probed); got != tc.want {
			t.Errorf("MeasuredContentType(%s, %q) = %q, want %q", tc.raw, tc.announced, got, tc.want)
		}
	}
}
