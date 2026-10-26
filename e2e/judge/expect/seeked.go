package expect

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stupside/castor/e2e/judge"
	"github.com/stupside/castor/e2e/receiver"
)

// keyframeSlack is how far from the asked position the stream may resume, landing on a keyframe.
const keyframeSlack = 2 * time.Second

// Seeked holds that castor served the viewer's jump from the position asked, and the device played on from there to the end.
type Seeked struct{}

func (Seeked) Name() string { return "seeked" }

func (Seeked) Judge(e judge.Evidence) []string {
	k := e.Received.Seek
	switch {
	case k == nil:
		return []string{"playback ended before the viewer jumped"}
	case !k.Offered:
		return []string{fmt.Sprintf("the hand-off offered no time seek, so the viewer could not jump to %v", k.To)}
	case k.Status != http.StatusOK:
		return []string{fmt.Sprintf("the jump to %v was answered %d, want 200", k.To, k.Status)}
	}
	start, ok := nptStart(k.Answer)
	moved := k.After - k.Before
	return slices.Concat(
		judge.FailIf(!ok || (start-k.To).Abs() > keyframeSlack, fmt.Sprintf("the jump to %v was answered with range %q, want it to start about there", k.To, k.Answer)),
		judge.FailIf((moved-k.To).Abs() > keyframeSlack, fmt.Sprintf("the first picture after the jump is %v past the first before it, want about %v", moved, k.To)),
		played(e),
	)
}

// endings say how a playback that did not reach the end of the media ended.
var endings = map[receiver.Playback]string{receiver.Stopped: "was stopped", receiver.Starved: "ran dry"}

// played holds that the device played on to the end of the media, with castor still there.
func played(e judge.Evidence) []string {
	if ending, ok := endings[e.Received.Playback]; ok {
		return []string{fmt.Sprintf("after the jump the device %s before the end of the media", ending)}
	}
	return judge.FailIf(e.Exited.Before(e.Received.Ended),
		fmt.Sprintf("castor exited %v before the device finished playing after the jump", e.Received.Ended.Sub(e.Exited).Round(time.Second)))
}

// nptStart is where a TimeSeekRange answer's npt range starts.
func nptStart(answer string) (time.Duration, bool) {
	spec, ok := strings.CutPrefix(answer, "npt=")
	start, _, ranged := strings.Cut(spec, "-")
	seconds, err := strconv.ParseFloat(start, 64)
	if !ok || !ranged || err != nil {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}
