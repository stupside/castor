package quirk

import (
	"fmt"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/strategy"
)

// Weighs builds a quirk that encodes the picture at a constant rate in bits a second, as in `weighs: 3M`; of a ladder, the tallest rung weighs it.
type Weighs struct{}

func (Weighs) Name() string { return "weighs" }

func (Weighs) Build(settings yaml.Node) (origin.Quirk, error) {
	var text string
	if err := strategy.Decode(settings, &text); err != nil {
		return nil, err
	}
	bits, err := strategy.Rate(text)
	if err != nil {
		return nil, err
	}
	return weighs{bits: bits}, nil
}

type weighs struct{ bits int }

func (weighs) Name() string { return "weighs" }

func (w weighs) Bend(s *origin.Stream) error {
	if s.Video.Name() != "h264" {
		return fmt.Errorf("only an h264 picture can be made to weigh a rate, not %s", s.Video.Name())
	}
	// The tallest rung weighs the rate; each smaller one weighs it by the share of the pixels it has.
	tallest := float64(s.Height() * s.Height())
	for i, height := range s.Heights {
		bits := int(float64(w.bits) * float64(height*height) / tallest)
		rate, at := strconv.Itoa(bits), ":v:"+strconv.Itoa(i)
		s.VideoOut = append(s.VideoOut, "-b"+at, rate, "-minrate"+at, rate, "-maxrate"+at, rate, "-bufsize"+at, strconv.Itoa(bits/2))
	}
	s.VideoOut = append(s.VideoOut, "-x264-params", "nal-hrd=cbr:force-cfr=1")
	return nil
}
