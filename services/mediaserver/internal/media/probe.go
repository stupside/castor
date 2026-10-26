package media

import (
	"context"
	"maps"
	"slices"
	"time"
)

type ProbeInfo struct {
	ContentType string
	// SourceUnwrapped reports that probing had to decode a nonstandard HLS segment envelope.
	SourceUnwrapped bool
	BitRate         int64         // the container's own top-level rate, 0 where ffprobe reports none
	Duration        time.Duration // the declared runtime, 0 for a live edge and where none is reported

	// Presence records selected stream descriptors even when their codec or
	// dimensions are unknown. Missing parameters do not establish absence.
	VideoPresent bool
	AudioPresent bool

	VideoCodec    Codec // e.g. CodecH264, CodecHEVC
	VideoProfile  Profile
	VideoHeight   int
	VideoBitDepth int  // derived from pix_fmt (8, 10, 12)
	VideoHDR      bool // PQ (smpte2084) or HLG (arib-std-b67) transfer
	// VideoLevel is the codec level in ffprobe's units (H.264 x10, so 42 is 4.2), 0 if unknown.
	VideoLevel int
	// VideoInterlaced is a picture coded as fields, which a device that does not deinterlace shows combed.
	VideoInterlaced bool
	// VideoRotation is the display matrix's turn in degrees; no container castor serves carries it.
	VideoRotation int

	// VideoStartOffset is stream start_time minus its input's format start_time.
	// Add the program input offset to locate the picture on the PCM clock.
	VideoStartOffset *time.Duration

	AudioCodec      Codec   // e.g. CodecAAC, CodecAC3
	AudioChannels   int     // channel count (2 = stereo, 6 = 5.1, 8 = 7.1), 0 if unknown
	AudioSampleRate int     // Hz, 0 if unknown
	AudioBitrate    Bitrate // what the track alone weighs, 0 if unknown
	// AudioTracks are the input's audio tracks in ffmpeg's kind-relative order.
	AudioTracks []AudioTrack

	VideoHeights []int

	// ProgramHeights is the tallest picture of each program by id, 0 for one without a picture.
	ProgramHeights map[int]int

	// Start is the first timestamp the source carries; InputStarts is each input's, for a program read from several.
	Start       time.Duration
	StartKnown  bool // distinguishes a measured zero timestamp from an absent clock
	InputStarts map[InputID]time.Duration
}

type AudioTrack struct {
	Language   string
	Codec      Codec
	Channels   int
	SampleRate int
	Bitrate    Bitrate // what the track alone weighs, 0 if unknown
	Default    bool
}

// TakeVideo is p with every picture fact taken from m; a new video field is carried here or nowhere.
func (p ProbeInfo) TakeVideo(m ProbeInfo) ProbeInfo {
	p.VideoPresent = m.VideoPresent
	p.VideoCodec, p.VideoProfile, p.VideoHeight, p.VideoBitDepth = m.VideoCodec, m.VideoProfile, m.VideoHeight, m.VideoBitDepth
	p.VideoHDR, p.VideoLevel = m.VideoHDR, m.VideoLevel
	p.VideoStartOffset = m.VideoStartOffset
	p.VideoInterlaced, p.VideoRotation = m.VideoInterlaced, m.VideoRotation
	p.VideoHeights, p.ProgramHeights = slices.Clone(m.VideoHeights), maps.Clone(m.ProgramHeights)
	return p
}

// TakeAudio is p with every sound fact taken from m.
func (p ProbeInfo) TakeAudio(m ProbeInfo) ProbeInfo {
	p.AudioPresent = m.AudioPresent
	p.AudioTracks = slices.Clone(m.AudioTracks)
	return p.Hearing(m.heard())
}

// Hearing is p with t as the sound it plays; a new track fact is carried here and in heard, or nowhere.
func (p ProbeInfo) Hearing(t AudioTrack) ProbeInfo {
	p.AudioCodec, p.AudioChannels, p.AudioSampleRate, p.AudioBitrate = t.Codec, t.Channels, t.SampleRate, t.Bitrate
	return p
}

// heard is the sound p plays, as the track Hearing took it from.
func (p ProbeInfo) heard() AudioTrack {
	return AudioTrack{Codec: p.AudioCodec, Channels: p.AudioChannels, SampleRate: p.AudioSampleRate, Bitrate: p.AudioBitrate}
}

// HasVideo reports whether video is present. A known codec also establishes
// presence for facts assembled from selected track metadata rather than
// directly from a probe's stream descriptors.
func (p ProbeInfo) HasVideo() bool { return p.VideoPresent || p.VideoCodec != "" }
func (p ProbeInfo) HasAudio() bool { return p.AudioPresent || p.AudioCodec != "" }

func (p ProbeInfo) Clone() ProbeInfo {
	if p.VideoStartOffset != nil {
		offset := *p.VideoStartOffset
		p.VideoStartOffset = &offset
	}
	p.VideoHeights = slices.Clone(p.VideoHeights)
	p.AudioTracks = slices.Clone(p.AudioTracks)
	p.ProgramHeights = maps.Clone(p.ProgramHeights)
	p.InputStarts = maps.Clone(p.InputStarts)
	return p
}

// Prober measures the tracks a reader will actually map.
type Prober interface {
	Probe(ctx context.Context) (ProbeInfo, Reach, error)
}

// Reach is how far an attempt to read a source got, as a fact about the ORIGIN rather than about the media.
type Reach int

const (
	ReachUnproven Reach = iota
	// ReachOpened is the origin serving the source. Only the link is proved live by it.
	ReachOpened
	// ReachRefused is the origin answering with a final no (401, 403, 404, 410).
	ReachRefused
)

func (r Reach) String() string {
	switch r {
	case ReachOpened:
		return "opened"
	case ReachRefused:
		return "refused"
	default:
		return "unproven"
	}
}
