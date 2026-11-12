package container

import (
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Adaptation is what a track needs so the container it is written into accepts it.
type Adaptation struct {
	// Filters are bitstream filters, in the order ffmpeg chains them.
	Filters []string
	// MovFlags are tokens merged into -movflags.
	MovFlags []string
	// OutputArgs go after the codec options and before -f, where their placement matters.
	OutputArgs []string
}

// Adapt sums every adaptation a track of kind landing as codec in into needs; copying tells a pass-through from an
// encode, and dolbyVision a picture carrying a Dolby Vision configuration.
func Adapt(kind media.TrackKind, codec media.Codec, into Format, copying, dolbyVision bool) Adaptation {
	s := subject{codec: codec, into: into, copying: copying, dolbyVision: dolbyVision}
	table := videoAdaptations
	if kind == media.TrackAudio {
		table = audioAdaptations
	}
	var a Adaptation
	for _, row := range table {
		if !row.when(s) {
			continue
		}
		a.Filters = append(a.Filters, row.filters...)
		a.MovFlags = append(a.MovFlags, row.movFlags...)
		a.OutputArgs = append(a.OutputArgs, row.outputArgs...)
	}
	return a
}

// subject is what a row matches on; deliberately not the source container, which a spool re-frames.
type subject struct {
	codec       media.Codec
	into        Format
	copying     bool
	dolbyVision bool
}

// adaptation is one codec-to-container row; rows that apply add up.
type adaptation struct {
	when       func(subject) bool
	filters    []string
	movFlags   []string
	outputArgs []string
}

func codecIn(codecs ...media.Codec) func(subject) bool {
	return func(s subject) bool { return slices.Contains(codecs, s.codec) }
}

// framedAs matches the destination's framing, never FramingUnknown.
func framedAs(f media.Framing) func(subject) bool {
	return func(s subject) bool { return f != media.FramingUnknown && s.into.Framing == f }
}

// muxedBy matches the destination muxer, for facts of one muxer rather than of a framing.
func muxedBy(muxers ...string) func(subject) bool {
	return func(s subject) bool { return s.into.Muxer != "" && slices.Contains(muxers, s.into.Muxer) }
}

// dolbyVisionEntry is a copied Dolby Vision picture bound for a player that reads Dolby Vision only from dvh1.
func dolbyVisionEntry(s subject) bool { return s.copying && s.dolbyVision && s.into.DolbyVisionEntry }

func not(p func(subject) bool) func(subject) bool { return func(s subject) bool { return !p(s) } }

func all(preds ...func(subject) bool) func(subject) bool {
	return func(s subject) bool {
		return !slices.ContainsFunc(preds, func(p func(subject) bool) bool { return !p(s) })
	}
}

// copying matches a pass-through: a repack belongs to a copy, an encoder already writes the right framing.
func copying(s subject) bool { return s.copying }

// audioAdaptations: MP3, MP2, Opus and DTS need nothing.
var audioAdaptations = []adaptation{
	{
		// AAC is ADTS inside MPEG-TS and an AudioSpecificConfig inside mp4.
		when:    all(copying, codecIn(media.CodecAAC), framedAs(media.FramingOutOfBand)),
		filters: []string{"aac_adtstoasc"},
	},
	{
		// delay_moov holds the unfragmented header until the first frame, which these codecs need to describe themselves.
		when:       all(codecIn(media.CodecAC3, media.CodecEAC3, media.CodecTrueHD), muxedBy(ffmpeg.FormatMP4)),
		movFlags:   []string{"delay_moov"},
		outputArgs: []string{"-frag_duration", "1000000"},
	},
}

// videoAdaptations: 10-bit and HDR copy cleanly and need nothing.
var videoAdaptations = []adaptation{
	{
		// HEVC lands tagged hev1; Apple and Chromecast expect hvc1.
		when:       all(codecIn(media.CodecHEVC), framedAs(media.FramingOutOfBand), not(dolbyVisionEntry)),
		outputArgs: []string{"-tag:v", "hvc1"},
	},
	{
		// The dvvC/dvcC box rides along either way; only this sample entry makes such a player show it.
		when:       all(codecIn(media.CodecHEVC), framedAs(media.FramingOutOfBand), dolbyVisionEntry),
		outputArgs: []string{"-tag:v", "dvh1"},
	},
}
