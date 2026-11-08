// Package container describes the containers castor can serve, and which codecs each one carries.
package container

import (
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// DeliveryKind determines how server hands stream to device; per-format data.
type DeliveryKind int

const (
	// DeliverStream: one growing output from byte 0 (MPEG-TS, frag mp4).
	DeliverStream DeliveryKind = iota
	// DeliverSegmented is a playlist plus rolling segments.
	DeliverSegmented
)

// Format is a container castor produces: its type, extension, muxer, and how it is delivered.
type Format struct {
	ContentType string
	Extension   string
	Muxer       string
	Delivery    DeliveryKind
	// Framing: how container keeps decoder config; zero is deliberate (see media.Framing).
	Framing media.Framing
	// Tuning: fixed options (not codec-dependent); on row to avoid silent drops.
	Tuning Tuning
	// DolbyVisionEntry marks a Dolby Vision copy dvh1 rather than hvc1, for a player that reads it only there.
	DolbyVisionEntry bool
}

// Tuning is the fixed output configuration of one muxer.
type Tuning struct {
	// MovFlags: base -movflags token set; nil for muxers without option.
	MovFlags []string
	// Args are the muxer's fixed output options, emitted after -f <muxer>.
	Args []string
	// Output: muxer target (pipe for stream, playlist for segmented).
	Output string
}

// formats is every container castor produces, keyed by content type.
var formats = map[string]Format{
	media.MPEGTS: {
		ContentType: media.MPEGTS, Extension: ".ts", Muxer: ffmpeg.FormatMPEGTS, Delivery: DeliverStream, Framing: media.FramingInBand,
		Tuning: Tuning{
			Args:   ffmpeg.MPEGTSResetArgs,
			Output: ffmpeg.StdoutPipe,
		},
	},
	media.MP4: {
		ContentType: media.MP4, Extension: ".mp4", Muxer: ffmpeg.FormatMP4, Delivery: DeliverStream, Framing: media.FramingOutOfBand,
		Tuning: Tuning{
			// Pipe needs fragmented mp4; empty_moov disables auto BSF (AAC filter hand-written).
			MovFlags: []string{"frag_keyframe", "empty_moov", "default_base_moof"},
			Output:   ffmpeg.StdoutPipe,
		},
	},
}

// For is the format castor produces for contentType, ok false for one it cannot.
func For(contentType string) (Format, bool) {
	f, ok := formats[contentType]
	return f, ok
}
