// Package wire translates castor's own types to the media contract and back, with no state and no I/O.
package wire

import (
	"errors"
	"net/url"

	"google.golang.org/protobuf/proto"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

var audioCodecs = map[mediav1.AudioCodec]media.Codec{
	mediav1.AudioCodec_AUDIO_CODEC_AAC:    media.CodecAAC,
	mediav1.AudioCodec_AUDIO_CODEC_AC3:    media.CodecAC3,
	mediav1.AudioCodec_AUDIO_CODEC_EAC3:   media.CodecEAC3,
	mediav1.AudioCodec_AUDIO_CODEC_MP3:    media.CodecMP3,
	mediav1.AudioCodec_AUDIO_CODEC_VORBIS: media.CodecVorbis,
}

var h264Profiles = map[mediav1.H264Support_Profile]media.Profile{
	mediav1.H264Support_PROFILE_BASELINE:             media.ProfileBaseline,
	mediav1.H264Support_PROFILE_CONSTRAINED_BASELINE: media.ProfileConstrainedBaseline,
	mediav1.H264Support_PROFILE_MAIN:                 media.ProfileMain,
	mediav1.H264Support_PROFILE_HIGH:                 media.ProfileHigh,
}

var hevcProfiles = map[mediav1.HEVCSupport_Profile]media.Profile{
	mediav1.HEVCSupport_PROFILE_MAIN:    media.ProfileMain,
	mediav1.HEVCSupport_PROFILE_MAIN_10: media.ProfileMain10,
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

var captionFormats = map[mediav1.CaptionFormat]media.CaptionFormat{
	mediav1.CaptionFormat_CAPTION_FORMAT_VTT: media.CaptionVTT,
	mediav1.CaptionFormat_CAPTION_FORMAT_SRT: media.CaptionSRT,
}

// Caption is c on the wire, its URL as the device reaches it.
func Caption(c media.Caption, reached *url.URL) *mediav1.DeviceCommand_Caption {
	format := mediav1.CaptionFormat_CAPTION_FORMAT_VTT
	if c.Format == media.CaptionSRT {
		format = mediav1.CaptionFormat_CAPTION_FORMAT_SRT
	}
	return &mediav1.DeviceCommand_Caption{Url: reached.String(), Format: format, Language: c.Language, Name: c.Name, Default: c.Default}
}

// FromCapabilities is what the wire says a device decodes, without what this server does not know.
func FromCapabilities(c *mediav1.Capabilities) media.Capabilities {
	out := media.Capabilities{SelfFetch: c.GetSelfFetch(), Provenance: c.GetProvenance(), Deinterlaces: c.GetDeinterlaces()}
	for _, f := range c.GetFormats() {
		var transport media.Transport
		switch t := f.GetTransport().(type) {
		case *mediav1.FormatSupport_Progressive:
			transport = map[mediav1.ProgressiveSupport_Container]media.Transport{
				mediav1.ProgressiveSupport_CONTAINER_MP4:    media.TransportMP4,
				mediav1.ProgressiveSupport_CONTAINER_MPEGTS: media.TransportMPEGTS,
				mediav1.ProgressiveSupport_CONTAINER_MKV:    media.TransportMKV,
				mediav1.ProgressiveSupport_CONTAINER_WEBM:   media.TransportWebM,
			}[t.Progressive.GetContainer()]
		case *mediav1.FormatSupport_Hls:
			switch t.Hls.GetSubtitles() {
			case mediav1.HLSSupport_SUBTITLES_UNSPECIFIED:
				transport = media.TransportHLS
			case mediav1.HLSSupport_SUBTITLES_WEBVTT:
				transport = media.TransportHLSWebVTT
			}
		}
		if transport == 0 {
			continue
		}
		var video *mediav1.VideoSupport
		var audio *mediav1.AudioSupport
		switch tracks := f.GetTracks().(type) {
		case *mediav1.FormatSupport_Av:
			video, audio = tracks.Av.GetVideo(), tracks.Av.GetAudio()
			if video == nil || audio == nil {
				continue
			}
		case *mediav1.FormatSupport_Video:
			video = tracks.Video
		case *mediav1.FormatSupport_Audio:
			audio = tracks.Audio
		default:
			continue
		}
		envelope := media.FormatSupport{Transport: transport, ServedHeaders: f.GetProgressive().GetServedHeaders()}
		if hls := f.GetHls(); hls != nil {
			for _, segment := range hls.GetExtraSegmentFormats() {
				switch segment {
				case mediav1.HLSSupport_EXTRA_SEGMENT_FORMAT_TIKTIKPX:
					envelope.ExtraSegments = append(envelope.ExtraSegments, media.HLSExtraSegmentTIKTIKPX)
				}
			}
		}
		if video != nil {
			v, ok := fromVideo(video)
			if !ok {
				continue
			}
			envelope.Video = v
		}
		if audio != nil {
			codec, ok := audioCodecs[audio.GetCodec()]
			if !ok {
				continue
			}
			envelope.Audio = media.AudioSupport{Codec: codec, MaxChannels: int(audio.GetMaxChannels())}
		}
		if envelope.Video.Codec == "" && envelope.Audio.Codec == "" {
			continue
		}
		for _, caption := range f.GetCaptions() {
			if format, ok := captionFormats[caption]; ok {
				envelope.Captions = append(envelope.Captions, format)
			}
		}
		out.Formats = append(out.Formats, envelope)
	}

	return out
}

func fromVideo(v *mediav1.VideoSupport) (media.VideoSupport, bool) {
	switch codec := v.GetCodec().(type) {
	case *mediav1.VideoSupport_H264:
		if codec.H264 == nil {
			return media.VideoSupport{}, false
		}
		vs := media.VideoSupport{Codec: media.CodecH264, MaxLevel: int(codec.H264.GetMaxLevel())}
		for _, p := range codec.H264.GetProfiles() {
			if profile, ok := h264Profiles[p]; ok {
				vs.Profiles = append(vs.Profiles, profile)
			}
		}
		if len(codec.H264.GetProfiles()) > 0 && len(vs.Profiles) == 0 {
			return media.VideoSupport{}, false
		}
		return vs, true
	case *mediav1.VideoSupport_Hevc:
		if codec.Hevc == nil {
			return media.VideoSupport{}, false
		}
		vs := media.VideoSupport{Codec: media.CodecHEVC, MaxLevel: int(codec.Hevc.GetMaxLevel())}
		for _, p := range codec.Hevc.GetProfiles() {
			if profile, ok := hevcProfiles[p]; ok {
				vs.Profiles = append(vs.Profiles, profile)
			}
		}
		if len(codec.Hevc.GetProfiles()) > 0 && len(vs.Profiles) == 0 {
			return media.VideoSupport{}, false
		}
		for _, depth := range codec.Hevc.GetBitDepths() {
			vs.BitDepths = append(vs.BitDepths, int(depth))
		}
		return vs, true
	case *mediav1.VideoSupport_Vp8:
		if codec.Vp8 != nil {
			return media.VideoSupport{Codec: media.CodecVP8}, true
		}
	}
	return media.VideoSupport{}, false
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
