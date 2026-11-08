package airplay

import (
	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

// capabilities are the same for every receiver of the family: castor states nothing it asked a receiver.
// HLS leads, since a third-party receiver's file player stops a fragmented MP4 at the end of what it first read
// (an LG C2 played 24 s of a 119 s stream); MP4 stays for the original files a receiver fetches whole.
func capabilities() *mediav1.Capabilities {
	caps := &mediav1.Capabilities{
		Provenance: "AirPlay video family envelopes (Apple TV and third-party receivers); model/firmware limits unverified; Native uses the receiver's playback verdict",
		SelfFetch:  true, NativePlaybackTrial: true,
	}
	hevc := &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_Hevc{Hevc: &mediav1.HEVCSupport{
		Profiles:  []mediav1.HEVCSupport_Profile{mediav1.HEVCSupport_PROFILE_MAIN, mediav1.HEVCSupport_PROFILE_MAIN_10},
		BitDepths: []uint32{8, 10},
	}}}
	audios := []mediav1.AudioCodec{mediav1.AudioCodec_AUDIO_CODEC_AAC, mediav1.AudioCodec_AUDIO_CODEC_AC3, mediav1.AudioCodec_AUDIO_CODEC_EAC3}
	for _, video := range []*mediav1.VideoSupport{device.H264Support(), hevc} {
		for _, audio := range audios {
			caps.Formats = append(caps.Formats, &mediav1.FormatSupport{
				Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{Subtitles: mediav1.HLSSupport_SUBTITLES_WEBVTT}},
				Tracks:    &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: video, Audio: &mediav1.AudioSupport{Codec: audio, MaxChannels: 6}}},
				Captions:  []mediav1.CaptionFormat{mediav1.CaptionFormat_CAPTION_FORMAT_VTT},
			})
		}
	}
	for _, video := range []*mediav1.VideoSupport{device.H264Support(), hevc} {
		for _, audio := range audios {
			caps.Formats = append(caps.Formats, &mediav1.FormatSupport{
				Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MP4}},
				Tracks:    &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: video, Audio: &mediav1.AudioSupport{Codec: audio, MaxChannels: 6}}},
			})
		}
	}
	return caps
}
