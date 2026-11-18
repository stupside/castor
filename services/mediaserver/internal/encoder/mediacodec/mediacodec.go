// Package mediacodec is Android's hardware encoders.
package mediacodec

import (
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/encoder"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Family is MediaCodec's H.264 and HEVC encoders, declared by the host because a test encode cannot tell it from the software encoder Android hands ffmpeg.
type Family struct{}

func (Family) Encoders() []encoder.Encoder {
	flags := []string{"-pix_fmt", "nv12", "-bitrate_mode", "vbr"}
	return []encoder.Encoder{
		{Name: "h264_mediacodec", Codec: media.CodecH264, Hardware: true, Flags: flags, DeclaredOnly: true, AverageRate: true, GOPKeyframes: true, SingleIDROutOfBand: true, LevelSpelling: h264Level},
		{Name: "hevc_mediacodec", Codec: media.CodecHEVC, Hardware: true, Flags: flags, DeclaredOnly: true, AverageRate: true, GOPKeyframes: true, SingleIDROutOfBand: true, LevelSpelling: hevcLevel},
	}
}

// h264Level drops the ".0" of a level: 4.0 is 4.
func h264Level(level string) string { return strings.TrimSuffix(level, ".0") }

// hevcLevel is a level in HEVC's main tier: 4.1 is m4.1.
func hevcLevel(level string) string { return "m" + strings.TrimSuffix(level, ".0") }
