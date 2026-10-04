// Package wire translates castor's own types to the media contract and back, with no state and no I/O.
package wire

import (
	"errors"

	"google.golang.org/protobuf/proto"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

var videoCodecs = map[mediav1.VideoCodec]media.Codec{
	mediav1.VideoCodec_VIDEO_CODEC_H264: media.CodecH264,
	mediav1.VideoCodec_VIDEO_CODEC_HEVC: media.CodecHEVC,
	mediav1.VideoCodec_VIDEO_CODEC_VP8:  media.CodecVP8,
}

var audioCodecs = map[mediav1.AudioCodec]media.Codec{
	mediav1.AudioCodec_AUDIO_CODEC_AAC:    media.CodecAAC,
	mediav1.AudioCodec_AUDIO_CODEC_AC3:    media.CodecAC3,
	mediav1.AudioCodec_AUDIO_CODEC_EAC3:   media.CodecEAC3,
	mediav1.AudioCodec_AUDIO_CODEC_MP3:    media.CodecMP3,
	mediav1.AudioCodec_AUDIO_CODEC_VORBIS: media.CodecVorbis,
}

var profiles = map[mediav1.Profile]media.Profile{
	mediav1.Profile_PROFILE_BASELINE:             media.ProfileBaseline,
	mediav1.Profile_PROFILE_CONSTRAINED_BASELINE: media.ProfileConstrainedBaseline,
	mediav1.Profile_PROFILE_MAIN:                 media.ProfileMain,
	mediav1.Profile_PROFILE_MAIN_10:              media.ProfileMain10,
	mediav1.Profile_PROFILE_HIGH:                 media.ProfileHigh,
}

// Container is contentType on the wire, unspecified for one the contract does not name.
func Container(contentType string) mediav1.Container {
	values := mediav1.Container_CONTAINER_UNSPECIFIED.Descriptor().Values()
	for i := range values.Len() {
		if c := mediav1.Container(values.Get(i).Number()); contentType != "" && mime(c) == contentType {
			return c
		}
	}
	return mediav1.Container_CONTAINER_UNSPECIFIED
}

// mime is the content type the contract names c by, empty for one it does not.
func mime(c mediav1.Container) string {
	v := c.Descriptor().Values().ByNumber(c.Number())
	if v == nil {
		return ""
	}
	return proto.GetExtension(v.Options(), mediav1.E_Mime).(string)
}

// FromCapabilities is what the wire says a device decodes, without what this server does not know.
func FromCapabilities(c *mediav1.Capabilities) media.Capabilities {
	out := media.Capabilities{
		SelfFetch:       c.GetSelfFetch(),
		ServedContainer: mime(c.GetServedContainer()),
		Deinterlaces:    c.GetDeinterlaces(),
		ServedHeaders:   c.GetServedHeaders(),
	}
	for _, ct := range c.GetContainers() {
		if m := mime(ct); m != "" {
			out.Containers = append(out.Containers, m)
		}
	}
	for _, v := range c.GetVideo() {
		if vs, ok := fromVideo(v); ok {
			out.Video = append(out.Video, vs)
		}
	}
	for _, a := range c.GetAudio() {
		if codec, ok := audioCodecs[a.GetCodec()]; ok {
			out.Audio = append(out.Audio, media.AudioSupport{Codec: codec, MaxChannels: int(a.GetMaxChannels())})
		}
	}
	return out
}

func fromVideo(v *mediav1.VideoSupport) (media.VideoSupport, bool) {
	codec, ok := videoCodecs[v.GetCodec()]
	if !ok {
		return media.VideoSupport{}, false
	}
	vs := media.VideoSupport{Codec: codec, MaxLevel: int(v.GetMaxLevel())}
	for _, p := range v.GetProfiles() {
		if profile, ok := profiles[p]; ok {
			vs.Profiles = append(vs.Profiles, profile)
		}
	}
	// No profile left would read as any profile, wider than the device said.
	if len(v.GetProfiles()) > 0 && len(vs.Profiles) == 0 {
		return media.VideoSupport{}, false
	}
	for _, b := range v.GetBitDepths() {
		vs.BitDepths = append(vs.BitDepths, int(b))
	}
	return vs, true
}

// FromDeviceError is how the wire says a device failed, a *media.Gone for one that went away.
func FromDeviceError(e *mediav1.DeviceError) error {
	if g := e.GetGone(); g != nil {
		gone := &media.Gone{Device: g.GetDevice(), Observed: g.GetObserved()}
		if g.GetCause() != "" {
			gone.Err = errors.New(g.GetCause())
		}
		return gone
	}
	return errors.New(e.GetMessage())
}
