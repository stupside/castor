// Package videotoolbox is Apple's hardware encoders.
package videotoolbox

import (
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/encoder"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Family is VideoToolbox's H.264 and HEVC encoders.
type Family struct{}

func (Family) Encoders() []encoder.Encoder {
	flags := []string{"-pix_fmt", "yuv420p", "-g", "600"}
	return []encoder.Encoder{
		// VideoToolbox can emit SEI that its FFmpeg wrapper cannot parse when it
		// merges embedded A53 captions. Drop that caption side data on re-encode
		// instead of aborting the video stream.
		{Name: "h264_videotoolbox", Codec: media.CodecH264, Hardware: true, Flags: slices.Concat(flags, []string{"-a53cc", "0"})},
		{Name: "hevc_videotoolbox", Codec: media.CodecHEVC, Hardware: true, Flags: flags},
	}
}
