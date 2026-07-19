package cast

import (
	"testing"

	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/media"
)

func TestSelectVideoEncoder(t *testing.T) {
	renderer := func(codecs ...media.Codec) media.Renderer {
		var r media.Renderer
		for _, c := range codecs {
			r.Video = append(r.Video, media.VideoSupport{Codec: c})
		}
		return r
	}
	fixed := func(m map[media.Codec]ffmpeg.Encoder) func(media.Codec) (ffmpeg.Encoder, bool) {
		return func(c media.Codec) (ffmpeg.Encoder, bool) { e, ok := m[c]; return e, ok }
	}

	hevcHW := ffmpeg.Encoder{Name: "hevc_videotoolbox", Codec: media.CodecHEVC, Hardware: true}
	hevcSW := ffmpeg.Encoder{Name: "libx265", Codec: media.CodecHEVC}
	h264HW := ffmpeg.Encoder{Name: "h264_videotoolbox", Codec: media.CodecH264, Hardware: true}
	h264SW := ffmpeg.Encoder{Name: "libx264", Codec: media.CodecH264}

	tests := []struct {
		name  string
		caps  media.Renderer
		avail map[media.Codec]ffmpeg.Encoder
		want  string
	}{
		{
			name:  "HEVC renderer with hardware HEVC picks HEVC",
			caps:  renderer(media.CodecHEVC, media.CodecH264),
			avail: map[media.Codec]ffmpeg.Encoder{media.CodecHEVC: hevcHW, media.CodecH264: h264HW},
			want:  "hevc_videotoolbox",
		},
		{
			name:  "HEVC renderer with only software HEVC falls to H.264 (never software HEVC live)",
			caps:  renderer(media.CodecHEVC, media.CodecH264),
			avail: map[media.Codec]ffmpeg.Encoder{media.CodecHEVC: hevcSW, media.CodecH264: h264HW},
			want:  "h264_videotoolbox",
		},
		{
			name:  "H.264-only renderer uses H.264 even when hardware HEVC exists",
			caps:  renderer(media.CodecH264),
			avail: map[media.Codec]ffmpeg.Encoder{media.CodecHEVC: hevcHW, media.CodecH264: h264SW},
			want:  "libx264",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectVideoEncoder(tt.caps, fixed(tt.avail)); got.Name != tt.want {
				t.Errorf("selectVideoEncoder = %q, want %q", got.Name, tt.want)
			}
		})
	}
}

// TestPreferredCodecsHaveTargets guards the coupling between the codec ladder and
// the bitrate map: a preferred codec with no target would transcode unbounded
// (no -maxrate), silently reintroducing the rebuffering these targets fix.
func TestPreferredCodecsHaveTargets(t *testing.T) {
	for _, c := range codecPreference {
		if _, ok := dlnaVideoTargets[c]; !ok {
			t.Errorf("codec %q is in codecPreference but has no dlnaVideoTargets entry", c)
		}
	}
}
