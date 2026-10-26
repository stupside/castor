package invariant

import (
	"fmt"
	"slices"
	"time"

	"github.com/stupside/castor/e2e/judge"
	"github.com/stupside/castor/e2e/receiver"
)

// joinSlack is how far behind a live start a player may join: ffmpeg, like a device, starts three segments back from the edge.
const joinSlack = 3 * time.Second

// PlayedOut holds a cast watched to the end to the source's length and to decoding cleanly, and a stopping viewer to having stopped it.
type PlayedOut struct{}

func (PlayedOut) Name() string { return "length" }

func (PlayedOut) Judge(e judge.Evidence) []string {
	stopped := e.Received.Playback == receiver.Stopped
	if e.Viewer.WillStop() {
		return judge.FailIf(!stopped, "the viewer was to stop playback, but the media ended first: lengthen the stream")
	}
	p := e.Received.Played
	want := time.Duration(e.Origin.Stream.Seconds)*time.Second - e.Received.Begins()
	short := judge.DurationSlack
	if e.Origin.Live {
		short += joinSlack
	}
	return slices.Concat(
		judge.FailIf(p.Duration < want-short || p.Duration > want+judge.DurationSlack, fmt.Sprintf("played %v of a %v source", p.Duration, want)),
		judge.FailIf(len(p.DecodeErrors) > 0, fmt.Sprintf("the tape does not decode cleanly: %v", p.DecodeErrors)),
	)
}
