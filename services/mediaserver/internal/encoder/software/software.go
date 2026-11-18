// Package software is the CPU encoders, libx264 and libx265.
package software

import (
	"fmt"
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/encoder"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Preset is how much effort the CPU encoders spend on a picture, in x264's names; none slower than veryfast, which a live read cannot wait for.
type Preset string

const (
	Ultrafast Preset = "ultrafast"
	Superfast Preset = "superfast"
	Veryfast  Preset = "veryfast"
)

var presets = []Preset{Ultrafast, Superfast, Veryfast}

func (p *Preset) UnmarshalText(name []byte) error {
	if !slices.Contains(presets, Preset(name)) {
		return fmt.Errorf("%q is not a software preset castor offers: %v", name, presets)
	}
	*p = Preset(name)
	return nil
}

// Family is the CPU encoders, working at Preset.
type Family struct{ Preset Preset }

func (f Family) Encoders() []encoder.Encoder {
	flags := []string{"-preset", string(f.Preset), "-pix_fmt", "yuv420p"}
	return []encoder.Encoder{
		{Name: "libx264", Codec: media.CodecH264, Flags: flags},
		{Name: "libx265", Codec: media.CodecHEVC, Flags: slices.Concat(flags, []string{"-x265-params", "log-level=error"})},
	}
}
