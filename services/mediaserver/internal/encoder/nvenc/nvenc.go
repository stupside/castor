// Package nvenc is NVIDIA's hardware encoders.
package nvenc

import (
	"github.com/stupside/castor/services/mediaserver/internal/encoder"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Family is NVENC's H.264 and HEVC encoders.
type Family struct{}

func (Family) Encoders() []encoder.Encoder {
	return []encoder.Encoder{
		{Name: "h264_nvenc", Codec: media.CodecH264, Hardware: true, Flags: []string{"-preset", "p4", "-pix_fmt", "yuv420p"}},
		{Name: "hevc_nvenc", Codec: media.CodecHEVC, Hardware: true, Flags: []string{"-preset", "p4", "-pix_fmt", "yuv420p"}},
	}
}
