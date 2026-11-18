// Package amf is AMD's hardware encoders.
package amf

import (
	"github.com/stupside/castor/services/mediaserver/internal/encoder"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Family is AMF's H.264 and HEVC encoders.
type Family struct{}

func (Family) Encoders() []encoder.Encoder {
	return []encoder.Encoder{
		{Name: "h264_amf", Codec: media.CodecH264, Hardware: true, Flags: []string{"-quality", "speed", "-pix_fmt", "nv12"}},
		{Name: "hevc_amf", Codec: media.CodecHEVC, Hardware: true, Flags: []string{"-quality", "speed", "-pix_fmt", "nv12"}},
	}
}
