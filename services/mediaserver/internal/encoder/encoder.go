// Package encoder is the video encoders a host can make a codec with, and the pick of the best one that works.
package encoder

import "github.com/stupside/castor/services/mediaserver/internal/media"

// Encoder is one concrete way to produce a codec: the ffmpeg -c:v name plus its command fragments.
type Encoder struct {
	Name     string      // -c:v value, e.g. "libx264", "hevc_videotoolbox"
	Codec    media.Codec // the abstract codec produced, independent of the name
	Hardware bool        // GPU-backed: trusted only after a real test encode
	InitArgs []string    // emitted before the input: hardware device setup
	Filters  []string    // appended to the -vf chain: e.g. the GPU upload
	Flags    []string    // encoder-specific -c:v flags: preset, pix_fmt, GOP

	DeclaredOnly       bool                      // never tried on its own: the host must declare it
	AverageRate        bool                      // honors -b:v alone, no VBV ceiling
	GOPKeyframes       bool                      // places keyframes by -g alone, ignoring forced ones
	SingleIDROutOfBand bool                      // with the decoder config out of band, only its first frame is an IDR
	LevelSpelling      func(level string) string // how -level:v reads a level; nil reads it as given
}

// Level is level as e's -level:v reads it.
func (e Encoder) Level(level string) string {
	if e.LevelSpelling == nil {
		return level
	}
	return e.LevelSpelling(level)
}

// Family is the encoders one vendor offers on this host, each proven by a test encode before it is used.
type Family interface {
	Encoders() []Encoder
}
