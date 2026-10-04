package quirk

import (
	"errors"
	"fmt"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/strategy"
)

// SampleRate builds a quirk that encodes audio at a given rate in Hz, as in `sample-rate: 96000`.
type SampleRate struct{}

func (SampleRate) Name() string { return "sample-rate" }

func (SampleRate) Build(settings yaml.Node) (origin.Quirk, error) {
	var hz int
	if err := strategy.Decode(settings, &hz); err != nil {
		return nil, err
	}
	if hz < 8000 || hz > 192000 {
		return nil, fmt.Errorf("want a rate from 8000 to 192000 Hz, not %d", hz)
	}
	return sampleRate{hz: hz}, nil
}

type sampleRate struct{ hz int }

func (sampleRate) Name() string { return "sample-rate" }

func (r sampleRate) Bend(s *origin.Stream) error {
	if s.Audio == nil {
		return errors.New("a silent stream has no sample rate")
	}
	s.AudioOut = append(s.AudioOut, "-ar", strconv.Itoa(r.hz))
	return nil
}
