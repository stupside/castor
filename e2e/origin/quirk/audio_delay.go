// Package quirk bends a synthetic stream the way real cameras, encoders and broadcasts leave theirs.
package quirk

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/strategy"
)

// AudioDelay builds a quirk whose audio opens later than its picture, as in `audio-delay: 1s`.
type AudioDelay struct{}

func (AudioDelay) Name() string { return "audio-delay" }

func (AudioDelay) Build(settings yaml.Node) (origin.Quirk, error) {
	var delay time.Duration
	if err := strategy.Decode(settings, &delay); err != nil {
		return nil, err
	}
	if delay <= 0 {
		return nil, fmt.Errorf("want a positive duration such as 1s, not %s", delay)
	}
	return audioDelay{delay: delay}, nil
}

type audioDelay struct{ delay time.Duration }

func (audioDelay) Name() string { return "audio-delay" }

func (a audioDelay) Bend(s *origin.Stream) error {
	if s.Audio == nil {
		return errors.New("a silent stream has no audio to delay")
	}
	s.AudioIn = append(s.AudioIn, "-itsoffset", strconv.FormatFloat(a.delay.Seconds(), 'f', -1, 64))
	s.AudioDelay = a.delay
	return nil
}
