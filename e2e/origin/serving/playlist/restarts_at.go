package playlist

import (
	"fmt"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/origin/serving"
	"github.com/stupside/castor/e2e/strategy"
)

// Sequence is how a restarted encoder numbers what it publishes next.
type Sequence string

const (
	// Reset drops everything before the restart and numbers it from 0 again.
	Reset Sequence = "reset"
	// Continue keeps the numbering and marks the restart with EXT-X-DISCONTINUITY.
	Continue Sequence = "continue"
)

// RestartsAt builds an encoder that restarts at a segment with fresh timestamps, as in `restarts-at: {segment: 10, sequence: reset}`.
type RestartsAt struct{}

func (RestartsAt) Name() string { return "restarts-at" }

func (RestartsAt) Build(settings yaml.Node) (origin.Behaviour, error) {
	var r restart
	if err := strategy.Decode(settings, &r); err != nil {
		return nil, fmt.Errorf("restarts-at: %w", err)
	}
	if r.Segment < 1 || (r.Sequence != Reset && r.Sequence != Continue) {
		return nil, fmt.Errorf("restarts-at: want segment >= 1 and sequence one of %s, %s", Reset, Continue)
	}
	return r, nil
}

type restart struct {
	Segment  int      `yaml:"segment"`
	Sequence Sequence `yaml:"sequence"`
}

func (restart) Name() string { return "restarts-at" }

func (s restart) Wrap(next http.Handler, p origin.Published) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch index, numbered := serving.SegmentIndex(r.URL.Path); {
		case p.IsSegment(r) && numbered && index >= s.Segment:
			serving.Rewrite(next, w, r, func(ts []byte) ([]byte, bool, error) {
				return ts, true, rebase(ts, uint64(s.Segment)*90000)
			})
		case path.Ext(r.URL.Path) == ".m3u8":
			serving.Rewrite(next, w, r, func(playlist []byte) ([]byte, bool, error) { return s.restarted(string(playlist)), true, nil })
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// restarted is a media playlist as the encoder publishes it once it has restarted; one that has not reached it passes through.
func (s restart) restarted(playlist string) []byte {
	var indices []int
	for line := range serving.Lines(playlist) {
		if index, ok := segmentOf(line); ok {
			indices = append(indices, index)
		}
	}
	if !slices.Contains(indices, s.Segment) {
		// Once the restart slides out of a window, the encoder still numbers from where it restarted.
		if s.Sequence == Reset && len(indices) > 0 && slices.Min(indices) > s.Segment {
			return []byte(sequencePattern.ReplaceAllString(playlist, fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d", slices.Min(indices)-s.Segment)))
		}
		return []byte(playlist)
	}
	var out strings.Builder
	for line := range serving.Lines(playlist) {
		index, entry := segmentOf(line)
		switch {
		case strings.HasPrefix(line.Text, "#EXT-X-MEDIA-SEQUENCE") && s.Sequence == Reset:
			out.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
		case entry && index < s.Segment && s.Sequence == Reset:
		case entry && index == s.Segment && s.Sequence == Continue:
			out.WriteString("#EXT-X-DISCONTINUITY\n")
			out.WriteString(line.Text)
			out.WriteString(line.URI)
		default:
			out.WriteString(line.Text)
			out.WriteString(line.URI)
		}
	}
	return []byte(out.String())
}

// segmentOf is the index of the segment an entry lists, and false for any other line.
func segmentOf(line serving.Line) (int, bool) {
	if !line.Entry() {
		return 0, false
	}
	return serving.SegmentIndex(uriPath(line.URI))
}

var sequencePattern = regexp.MustCompile(`#EXT-X-MEDIA-SEQUENCE:\d+`)

// uriPath is the path of a playlist's URI line, without its query.
func uriPath(line string) string {
	uri, _, _ := strings.Cut(strings.TrimSpace(line), "?")
	return uri
}
