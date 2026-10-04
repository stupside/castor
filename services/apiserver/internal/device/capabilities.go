package device

import (
	"slices"

	"google.golang.org/protobuf/proto"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
)

// MIME is how a device protocol names container, as the contract states it; empty for one it does not know.
func MIME(container mediav1.Container) string {
	v := container.Descriptor().Values().ByNumber(container.Number())
	if v == nil {
		return ""
	}
	return proto.GetExtension(v.Options(), mediav1.E_Mime).(string)
}

type codecEnvelope struct {
	profiles  []mediav1.Profile
	bitDepths []uint32 // nil == 8-bit only
	maxLevel  uint32
}

var codecEnvelopes = map[mediav1.VideoCodec]codecEnvelope{
	// Level 4.2 (1080p60) is what every HD H.264 decoder castor targets is built to.
	mediav1.VideoCodec_VIDEO_CODEC_H264: {profiles: []mediav1.Profile{mediav1.Profile_PROFILE_CONSTRAINED_BASELINE, mediav1.Profile_PROFILE_BASELINE, mediav1.Profile_PROFILE_MAIN, mediav1.Profile_PROFILE_HIGH}, maxLevel: 42},
	mediav1.VideoCodec_VIDEO_CODEC_HEVC: {profiles: []mediav1.Profile{mediav1.Profile_PROFILE_MAIN, mediav1.Profile_PROFILE_MAIN_10}, bitDepths: []uint32{8, 10}},
	mediav1.VideoCodec_VIDEO_CODEC_VP8:  {}, // VP8 has no profile split in Castor's probe model; 8-bit is the default.
}

// VideoSupport is the envelope every family states for codec.
func VideoSupport(codec mediav1.VideoCodec) *mediav1.VideoSupport {
	env := codecEnvelopes[codec]
	return &mediav1.VideoSupport{
		Codec:     codec,
		Profiles:  slices.Clone(env.profiles),
		BitDepths: slices.Clone(env.bitDepths),
		MaxLevel:  env.maxLevel,
	}
}
