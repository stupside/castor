package fetch

import (
	"fmt"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

const configuredDeadline = 37 * time.Second

func TestThePolicyASourceShapeIsReadWith(t *testing.T) {
	for _, tt := range []struct {
		shape    media.Fetch
		deadline time.Duration
	}{
		{media.Fetch{Segmented: true, Framing: media.FramingOutOfBand, Live: true}, configuredDeadline},
		{media.Fetch{Live: true}, configuredDeadline},
		// Abandoning a fragment mid-read truncates what nothing can resynchronise.
		{media.Fetch{Segmented: true, Framing: media.FramingOutOfBand}, 0},
		{media.Fetch{Segmented: true, Framing: media.FramingUnknown}, 0},
		{media.Fetch{Segmented: true, Framing: media.FramingInBand}, configuredDeadline},
		{media.Fetch{}, configuredDeadline},
	} {
		t.Run(fmt.Sprintf("%+v", tt.shape), func(t *testing.T) {
			p := For(tt.shape, configuredDeadline)
			if p.Deadline != tt.deadline {
				t.Errorf("deadline %s, want %s", p.Deadline, tt.deadline)
			}
		})
	}
}

func TestACautiousReadGivesUpOnlyItsPace(t *testing.T) {
	for _, shape := range []media.Fetch{{Segmented: true, Framing: media.FramingOutOfBand}, {Segmented: true, Framing: media.FramingInBand}, {}} {
		t.Run(fmt.Sprintf("%+v", shape), func(t *testing.T) {
			was := For(shape, configuredDeadline)
			got, ok := cautious(was)
			if !ok || got.Pace != pacePlayback {
				t.Fatalf("cautious = %+v (%v), want playback pace with no burst", got.Pace, ok)
			}
			if got.Deadline != was.Deadline || got.SegmentRetries != was.SegmentRetries || got.Name == was.Name {
				t.Errorf("relaxing changed more than the pace: %+v from %+v", got, was)
			}
			if _, again := cautious(got); again {
				t.Error("a cautious read offered a second relaxation")
			}
		})
	}
}
