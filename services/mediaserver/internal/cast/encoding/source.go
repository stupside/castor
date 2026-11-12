package encoding

import (
	"fmt"
	"iter"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/probe"
)

// InputArgs is how ffmpeg opens an input of contentType, as the source format that claims it says.
type InputArgs func(contentType string, segmentRetries int) []string

// ProgramSource is a program bound to the fetch policy each of its inputs is read with.
type ProgramSource struct {
	program media.Program
	plan    fetch.Plan
	binary  ffmpeg.Binary
	open    InputArgs
}

// NewProgramSource snapshots a program and its complete fetch plan.
func NewProgramSource(program media.Program, plan fetch.Plan, binary ffmpeg.Binary, open InputArgs) (ProgramSource, error) {
	_, video := program.Track(media.TrackVideo)
	_, audio := program.Track(media.TrackAudio)
	if !video && !audio {
		return ProgramSource{}, fmt.Errorf("FFmpeg program source selects neither video nor audio")
	}
	// A representation is castor's to translate; ffmpeg reading the whole manifest would play something else.
	for _, input := range program.Inputs {
		if input.Representation != "" {
			return ProgramSource{}, fmt.Errorf("the %s input reads representation %q, which castor never republished for ffmpeg", input.ID, input.Representation)
		}
	}

	return ProgramSource{program: program.Clone(), plan: maps.Clone(plan), binary: binary, open: open}, nil
}

func (s ProgramSource) inputs() iter.Seq2[media.InputID, sourceInput] {
	return func(yield func(media.InputID, sourceInput) bool) {
		for _, input := range s.program.Inputs {
			if !yield(input.ID, sourceInput{
				url:         input.URL,
				headers:     input.Headers,
				contentType: input.ContentType,
				fetch:       s.plan[input.ID],
				offset:      s.program.Offsets[input.ID],
			}) {
				return
			}
		}
	}
}

func (s ProgramSource) outputArgs() []string {
	if s.program.EndPolicy == media.EndAtShortest {
		return []string{"-shortest"}
	}
	return nil
}

// ProbeInputs is each input as ffprobe must open it to measure what this source's read will.
func (s ProgramSource) ProbeInputs() []probe.Input {
	out := make([]probe.Input, 0, len(s.program.Inputs))
	for id, input := range s.inputs() {
		out = append(out, probe.Input{ID: id, URL: input.url.String(), Args: OpenArgs(s.open, input.headers, input.contentType, input.fetch)})
	}
	return out
}

type sourceInput struct {
	url         *url.URL
	headers     http.Header
	contentType string
	fetch       fetch.Policy
	offset      time.Duration
}

// paceArgs renders read pace as ffmpeg input flags (nil if unpaced).
func paceArgs(p fetch.Pace, catchup bool) []string {
	if p.Realtime <= 0 {
		return nil
	}
	args := []string{
		"-readrate", formatRate(p.Realtime),
		"-readrate_initial_burst", strconv.Itoa(int(p.Burst.Seconds())),
	}
	// An older binary catches up unbounded on its own; a newer one only at the rate it is given.
	if catchup && p.Catchup > p.Realtime {
		args = append(args, "-readrate_catchup", formatRate(p.Catchup))
	}
	return args
}

// formatRate formats realtime multiple for -readrate (at least one decimal place).
func formatRate(multiple float64) string {
	s := strconv.FormatFloat(multiple, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// formatSeconds spells a duration as ffmpeg's duration options take it: seconds, no unit suffix.
func formatSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64)
}

// candidateBackoff is the longest a candidate's probe waits to reconnect, so it retries a transient refusal twice within a second, then answers.
const candidateBackoff = 2 * time.Second

// OpenArgs is how a reader opens an input: the protocols castor allows, its fetch terms, its headers and its format's demuxer.
func OpenArgs(open InputArgs, headers http.Header, contentType string, policy fetch.Policy) []string {
	return openArgs(open, headers, contentType, policy, fetch.BackoffMax)
}

// CandidateArgs opens a candidate as its reader would, but one still refusing after candidateBackoff is unproven now, which is what choosing needs to know.
func CandidateArgs(open InputArgs, headers http.Header, contentType string, policy fetch.Policy) []string {
	return openArgs(open, headers, contentType, policy, candidateBackoff)
}

func openArgs(open InputArgs, headers http.Header, contentType string, policy fetch.Policy, backoff time.Duration) []string {
	args := append(ffmpeg.ProtocolArgs(), readArgs(policy, backoff)...)
	args = append(args, ffmpeg.HeaderArgs(headers)...)
	return append(args, open(contentType, policy.SegmentRetries)...)
}

// readArgs renders protocol-level fetch terms: the deadline, and reconnects waiting at most backoff.
func readArgs(p fetch.Policy, backoff time.Duration) []string {
	var args []string
	if p.Deadline > 0 {
		args = append(args, "-rw_timeout", strconv.FormatInt(p.Deadline.Microseconds(), 10))
	}
	return append(args,
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", strconv.Itoa(int(backoff.Seconds())),
		// Transient statuses are retried; any other refusal is the origin's answer.
		"-reconnect_on_http_error", "429,500,502,503,504",
	)
}

// demuxFlags are terms all inputs use (generate timestamps, drop corrupt packets).
var demuxFlags = []string{"-fflags", "+genpts+discardcorrupt"}

// sourceInputArgs renders each input with its fetch policy, headers, pace, and where it is read from.
func sourceInputArgs(source ProgramSource, keepTimeline bool) []string {
	var args []string
	// A seam's jump is a discontinuity, not a gap (the default 10s stretches it); ffmpeg holds it for the whole read.
	if source.program.Seamed() {
		args = append(args, "-dts_delta_threshold", "1")
	}
	for _, input := range source.inputs() {
		args = append(args, demuxFlags...)
		args = append(args, paceArgs(input.fetch.Pace, source.binary.Catchup)...)
		args = append(args, OpenArgs(source.open, input.headers, input.contentType, input.fetch)...)
		args = append(args, seekArgs(source.program.Start, input.offset, keepTimeline)...)
		args = append(args, "-i", input.url.String())
	}
	return args
}

// seekArgs open an input at start; offset realigns an input ffmpeg rebased to zero, which a kept timeline's raw timestamps already align.
func seekArgs(start, offset time.Duration, keepTimeline bool) []string {
	var args []string
	if start > 0 {
		args = append(args, "-ss", formatSeconds(start))
	}
	if offset != 0 && !keepTimeline {
		args = append(args, "-itsoffset", formatSeconds(offset))
	}
	return args
}

// sourceMap renders one selected, kind-relative track.
func sourceMap(source ProgramSource, kind media.TrackKind) (string, bool) {
	track, input, ok := source.program.TrackInput(kind)
	if !ok {
		return "", false
	}
	spec := string(kind[:1])
	if kind == media.TrackVideo {
		spec = "V"
	}
	optional := ""
	if track.Optional {
		optional = "?"
	}
	return fmt.Sprintf("%d:%s:%d%s", input, spec, track.Index, optional), true
}

func sourceMapArgs(source ProgramSource, video, audio bool) []string {
	var args []string
	if video {
		if mapped, ok := sourceMap(source, media.TrackVideo); ok {
			args = append(args, "-map", mapped)
		}
	}
	if audio {
		if mapped, ok := sourceMap(source, media.TrackAudio); ok {
			args = append(args, "-map", mapped)
		}
	}
	return args
}
