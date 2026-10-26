package codec

import (
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// copyName is the codec name "-c" takes to copy.
const copyName = "copy"

// encode is the re-encode one half of a program can be given.
type encode interface {
	VideoEncode | AudioEncode
	name() string
}

// Action is an explicit decision for one selected track. The zero value is an
// unfinished plan and cannot be rendered into an FFmpeg command.
type Action uint8

const (
	Unplanned Action = iota
	OmitTrack
	CopyTrack
	EncodeTrack
)

// Track distinguishes an absent track, a stream copy, and a re-encode.
type Track[E encode] struct {
	action Action
	enc    *E
}

func OmitVideo() Track[VideoEncode] { return Track[VideoEncode]{action: OmitTrack} }

func OmitAudio() Track[AudioEncode] { return Track[AudioEncode]{action: OmitTrack} }

func CopyVideo() Track[VideoEncode] { return Track[VideoEncode]{action: CopyTrack} }

func CopyAudio() Track[AudioEncode] { return Track[AudioEncode]{action: CopyTrack} }

func Encode[E encode](e E) Track[E] { return Track[E]{action: EncodeTrack, enc: &e} }

func (t Track[E]) Action() Action { return t.action }

// Encode returns the re-encode parameters, with ok false for other actions.
func (t Track[E]) Encode() (E, bool) {
	if t.enc == nil {
		var none E
		return none, false
	}
	return *t.enc, true
}

// Name describes the decision in logs; only copy and encode name a -c value.
func (t Track[E]) Name() string {
	switch t.action {
	case EncodeTrack:
		return (*t.enc).name()
	case CopyTrack:
		return copyName
	case OmitTrack:
		return "omit"
	default:
		return "unplanned"
	}
}

// VideoEncode is the video re-encode: the encoder and the picture it produces.
type VideoEncode struct {
	Encoder  ffmpeg.Encoder
	Profile  string
	Level    string
	MaxWidth int

	// Bitrate is the average target; Maxrate and Bufsize bound the instantaneous rate via VBV.
	Bitrate media.Bitrate
	Maxrate media.Bitrate
	Bufsize media.Bitrate

	// Quality is a constant-quality target (-crf); only set with rate ceilings (see floorVideoQuality).
	Quality int

	// MaxHeight caps the output height while preserving aspect ratio.
	MaxHeight media.HeightCap

	// KeyframeIntervalSec caps GOP length so a device joining mid-stream resyncs within this bound.
	KeyframeIntervalSec int

	// SubtitleTextFile burns the file's current contents into every frame via drawtext with reload=1.
	SubtitleTextFile string

	// Deinterlace weaves fields into frames for a device that would show them combed.
	Deinterlace bool
	// ToneMap maps an HDR picture to BT.709 SDR, since no device is known to engage HDR.
	ToneMap bool
	// MaxFrameRate drops frames above it; 0 keeps the source's rate.
	MaxFrameRate float64
}

func (e VideoEncode) name() string { return e.Encoder.Name }

// AudioEncode is the audio re-encode: the codec to produce and the shape to produce it in.
type AudioEncode struct {
	// Codec is the encoder to run, e.g. aac, ac3, eac3 (the abstract codec, not the encoder name).
	Codec media.Codec
	// Bitrate is the target, which is what the sound adds to the cast.
	Bitrate media.Bitrate
	// SampleRate is the output rate in Hz; 0 keeps the source rate.
	SampleRate int
	// Channels is the output layout; 0 keeps the source layout.
	Channels int
	// Resync retimes samples by their count, a clock that runs straight through seams where timestamps restart.
	Resync bool
}

func (e AudioEncode) name() string { return string(e.Codec) }
