// Package qsv is Intel's Quick Sync hardware encoders.
package qsv

import (
	"github.com/stupside/castor/services/mediaserver/internal/encoder"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Family is Quick Sync's H.264 and HEVC encoders.
type Family struct{}

func (Family) Encoders() []encoder.Encoder {
	return []encoder.Encoder{
		{Name: "h264_qsv", Codec: media.CodecH264, Hardware: true, Flags: []string{"-preset", "veryfast", "-pix_fmt", "nv12"}},
		{Name: "hevc_qsv", Codec: media.CodecHEVC, Hardware: true, Flags: []string{"-preset", "veryfast", "-pix_fmt", "nv12"}},
	}
}
