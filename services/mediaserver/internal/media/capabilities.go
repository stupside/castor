package media

import (
	"net/url"
	"slices"
)

// Capabilities contains atomic playback envelopes in preference order.
type Capabilities struct {
	Formats      []FormatSupport
	Provenance   string
	SelfFetch    bool
	Deinterlaces bool
}

// Served is the headers a response serving contentType carries, from the first format that serves it.
func (c Capabilities) Served(contentType string) map[string]string {
	for _, f := range c.Formats {
		if f.Transport.ContentType() == contentType {
			return f.ServedHeaders
		}
	}
	return nil
}

// Transport is a complete delivery variant, including its rendition support.
type Transport uint8

const (
	TransportMP4 Transport = iota + 1
	TransportMPEGTS
	TransportMKV
	TransportWebM
	TransportHLS
	TransportHLSWebVTT
)

func (t Transport) ContentType() string {
	switch t {
	case TransportMP4:
		return MP4
	case TransportMPEGTS:
		return MPEGTS
	case TransportMKV:
		return MKV
	case TransportWebM:
		return WebM
	case TransportHLS, TransportHLSWebVTT:
		return HLS
	}
	return ""
}

// CaptionFormat is how a caption track is written.
type CaptionFormat string

const (
	CaptionVTT CaptionFormat = "vtt"
	CaptionSRT CaptionFormat = "srt"
)

// Caption is a text track a device shows over the stream, fetched at URL like the stream is.
type Caption struct {
	URL      *url.URL
	Format   CaptionFormat
	Language string
	Name     string
	Default  bool
}

// VideoSupport is one video envelope a device decodes natively (codec, profile, bit depth, level).
type VideoSupport struct {
	Codec     Codec
	Profiles  []Profile // nil or empty = any profile
	BitDepths []int     // nil or empty = {8}
	// MaxLevel is the highest level decoded, in ffprobe's units (H.264 x10); 0 = no advertised ceiling.
	MaxLevel int
}

// AudioSupport is one audio codec a device decodes natively, up to MaxChannels channels.
type AudioSupport struct {
	Codec Codec
	// MaxChannels is the highest channel count the device decodes (0 = no advertised ceiling).
	MaxChannels int
}

// PlaybackSampleRate is the highest audio sample rate any device castor serves plays.
const PlaybackSampleRate = 48000

func (c Capabilities) CanCopyVideo(v ProbeInfo) bool {
	if v.VideoInterlaced && !c.Deinterlaces {
		return false
	}
	return slices.ContainsFunc(c.Formats, func(f FormatSupport) bool { return f.Video.accepts(v) })
}

func (c Capabilities) CanCopyAudio(p ProbeInfo) bool {
	if p.AudioSampleRate > PlaybackSampleRate {
		return false
	}
	return slices.ContainsFunc(c.Formats, func(f FormatSupport) bool { return f.Audio.accepts(p) })
}

func (s AudioSupport) accepts(p ProbeInfo) bool {
	if p.AudioCodec == "" || p.AudioCodec != s.Codec {
		return false
	}
	// Unknown source channel count (0) is trusted: don't force-transcode a matching codec with no layout.
	if s.MaxChannels > 0 && p.AudioChannels > s.MaxChannels {
		return false
	}
	return true
}

func (s VideoSupport) accepts(v ProbeInfo) bool {
	if v.VideoCodec == "" || v.VideoCodec != s.Codec {
		return false
	}
	if len(s.Profiles) > 0 && !slices.Contains(s.Profiles, v.VideoProfile) && !(v.VideoProfile == ProfileConstrainedBaseline && slices.Contains(s.Profiles, ProfileBaseline)) {
		return false
	}
	// Unknown source level (0) is trusted, as an unknown channel count is.
	if s.MaxLevel > 0 && v.VideoLevel > s.MaxLevel {
		return false
	}
	depths := s.BitDepths
	if len(depths) == 0 {
		depths = []int{8}
	}
	return slices.Contains(depths, v.VideoBitDepth)
}

// FormatSupport is one indivisible playback combination.
type FormatSupport struct {
	Transport     Transport
	Video         VideoSupport
	Audio         AudioSupport
	Captions      []CaptionFormat
	ExtraSegments []HLSExtraSegmentFormat
	ServedHeaders map[string]string
}

type HLSExtraSegmentFormat uint8

const (
	HLSExtraSegmentTIKTIKPX HLSExtraSegmentFormat = iota + 1
)

// canPlayHLSWith checks that one playback envelope accepts the tracks and
// all of the supplied nonstandard segment formats.
func (c Capabilities) canPlayHLSWith(p ProbeInfo, segments ...HLSExtraSegmentFormat) bool {
	return slices.ContainsFunc(c.Formats, func(f FormatSupport) bool {
		if f.Transport.ContentType() != HLS || !f.accepts(p, c.Deinterlaces) {
			return false
		}
		for _, segment := range segments {
			if !slices.Contains(f.ExtraSegments, segment) {
				return false
			}
		}
		return true
	})
}

// CanPlaySourceIn checks the source's observed HLS segment envelope as well as
// its tracks. Served output uses CanPlayIn because Castor has already unwrapped it.
func (c Capabilities) CanPlaySourceIn(container string, p ProbeInfo) bool {
	// The destination's HDR mode is not established by its codec capability.
	if p.VideoHDR {
		return false
	}
	if container == HLS && p.SourceUnwrapped {
		return c.canPlayHLSWith(p, HLSExtraSegmentTIKTIKPX)
	}
	return c.CanPlayIn(container, p)
}

func (c Capabilities) CanPlayIn(container string, p ProbeInfo) bool {
	return slices.ContainsFunc(c.Formats, func(f FormatSupport) bool {
		return f.Transport.ContentType() == container && f.accepts(p, c.Deinterlaces)
	})
}

func (f FormatSupport) accepts(p ProbeInfo, deinterlaces bool) bool {
	return (p.HasVideo() || p.HasAudio()) &&
		(!p.VideoInterlaced || deinterlaces) &&
		(!p.HasVideo() || f.Video.accepts(p)) &&
		(!p.HasAudio() || f.Audio.accepts(p))
}
