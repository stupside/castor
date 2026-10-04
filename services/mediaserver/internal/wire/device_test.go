package wire

import (
	"reflect"
	"testing"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestCapabilitiesMapVideoAndAudioEnumsIndependently(t *testing.T) {
	caps := &mediav1.Capabilities{
		Video: []*mediav1.VideoSupport{
			{Codec: mediav1.VideoCodec_VIDEO_CODEC_H264},
			{Codec: mediav1.VideoCodec_VIDEO_CODEC_HEVC},
			{Codec: mediav1.VideoCodec_VIDEO_CODEC_VP8},
		},
		Audio: []*mediav1.AudioSupport{
			{Codec: mediav1.AudioCodec_AUDIO_CODEC_AAC, MaxChannels: 2},
			{Codec: mediav1.AudioCodec_AUDIO_CODEC_AC3},
			{Codec: mediav1.AudioCodec_AUDIO_CODEC_EAC3},
			{Codec: mediav1.AudioCodec_AUDIO_CODEC_MP3},
			{Codec: mediav1.AudioCodec_AUDIO_CODEC_VORBIS},
		},
	}
	want := media.Capabilities{
		Video: []media.VideoSupport{{Codec: media.CodecH264}, {Codec: media.CodecHEVC}, {Codec: media.CodecVP8}},
		Audio: []media.AudioSupport{{Codec: media.CodecAAC, MaxChannels: 2}, {Codec: media.CodecAC3}, {Codec: media.CodecEAC3}, {Codec: media.CodecMP3}, {Codec: media.CodecVorbis}},
	}
	if got := FromCapabilities(caps); !reflect.DeepEqual(got, want) {
		t.Errorf("FromCapabilities = %+v, want %+v", got, want)
	}
}

func TestCapabilitiesSkipUnknownCodecsWithoutWideningProfiles(t *testing.T) {
	const unknown = 99
	caps := &mediav1.Capabilities{
		SelfFetch:       true,
		Containers:      []mediav1.Container{mediav1.Container_CONTAINER_MPEGTS, unknown},
		ServedContainer: mediav1.Container_CONTAINER_MP4,
		ServedHeaders:   map[string]string{"User-Agent": "Castor"},
		Deinterlaces:    true,
		Video: []*mediav1.VideoSupport{
			nil,
			{},
			{Codec: unknown},
			{Codec: mediav1.VideoCodec_VIDEO_CODEC_H264, Profiles: []mediav1.Profile{mediav1.Profile_PROFILE_HIGH, unknown}, MaxLevel: 42},
			{Codec: mediav1.VideoCodec_VIDEO_CODEC_HEVC, Profiles: []mediav1.Profile{unknown}},
			{Codec: mediav1.VideoCodec_VIDEO_CODEC_HEVC, Profiles: []mediav1.Profile{mediav1.Profile_PROFILE_MAIN_10}, BitDepths: []uint32{10}},
		},
		Audio: []*mediav1.AudioSupport{nil, {}, {Codec: unknown}, {Codec: mediav1.AudioCodec_AUDIO_CODEC_AAC, MaxChannels: 2}},
	}
	want := media.Capabilities{
		SelfFetch:       true,
		Containers:      []string{media.MPEGTS},
		ServedContainer: media.MP4,
		ServedHeaders:   caps.ServedHeaders,
		Deinterlaces:    true,
		Video: []media.VideoSupport{
			{Codec: media.CodecH264, Profiles: []media.Profile{media.ProfileHigh}, MaxLevel: 42},
			{Codec: media.CodecHEVC, Profiles: []media.Profile{media.ProfileMain10}, BitDepths: []int{10}},
		},
		Audio: []media.AudioSupport{{Codec: media.CodecAAC, MaxChannels: 2}},
	}
	if got := FromCapabilities(caps); !reflect.DeepEqual(got, want) {
		t.Errorf("FromCapabilities = %+v, want %+v", got, want)
	}
	if got := FromCapabilities(nil); !reflect.DeepEqual(got, media.Capabilities{}) {
		t.Errorf("FromCapabilities(nil) = %+v, want empty capabilities", got)
	}
}
