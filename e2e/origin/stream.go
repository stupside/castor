package origin

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stupside/castor/e2e/strategy"
)

// Stream is a resolved spec: the strategies it names, bound. Audio is nil for a silent stream.
type Stream struct {
	Packager Packager
	Video    VideoCodec
	// Heights are the renditions, ascending; the last is the tallest.
	Heights  []int
	Depth    int
	Transfer Transfer
	Audio    AudioCodec
	Channels int
	Carriage Carriage
	Segments SegmentsSpec
	Seconds  int
	Entry    EntrySpec

	// Knobs a quirk bends; zero is the plain synthetic source.
	Rate     string   // testsrc frame rate, as ffmpeg takes it ("15", "59.94", "24000/1001")
	VideoIn  []string // input options before the testsrc -i
	AudioIn  []string // input options before the sine -i
	Filters  []string // filters after the transfer tag, before the ladder splits
	VideoOut []string // after the video encoder's arguments; a repeated option wins
	AudioOut []string // after the audio encoder's arguments
	MuxOut   []string // before the packager's arguments

	// Facts a quirk records for the judge.
	Rotation   int
	AudioDelay time.Duration
	Interlaced bool
	Chroma     int
}

// Height is the tallest rendition.
func (s Stream) Height() int { return s.Heights[len(s.Heights)-1] }

// Rung is the tallest rendition within ceiling, and false when every rendition is taller.
func (s Stream) Rung(ceiling int) (int, bool) {
	for _, h := range slices.Backward(s.Heights) {
		if h <= ceiling {
			return h, true
		}
	}
	return 0, false
}

func (s Stream) layout() Layout {
	return Layout{Rungs: len(s.Heights), Audio: s.Audio != nil, Carriage: s.Carriage, SegmentExt: s.Segments.Extension}
}

// Quirk bends a resolved stream the way a real encoder or camera leaves it: it sets encode knobs and records facts.
type Quirk interface {
	strategy.Named
	Bend(s *Stream) error
}

// DefaultRate is the synthetic source's frame rate when no quirk sets one.
const DefaultRate = "15"

// FPS is the source's frame rate as a number, reading ffmpeg's rational form too.
func (s Stream) FPS() float64 {
	rate := cmp.Or(s.Rate, DefaultRate)
	if num, den, ok := strings.Cut(rate, "/"); ok {
		n, _ := strconv.ParseFloat(num, 64)
		d, _ := strconv.ParseFloat(den, 64)
		return n / d
	}
	f, _ := strconv.ParseFloat(rate, 64)
	return f
}
