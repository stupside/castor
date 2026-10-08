// Package playlist holds HLS origins that publish their playlists the hard way: live, restarted, spliced, stale or rewritten.
package playlist

import (
	"fmt"
	"maps"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/origin/serving"
	"github.com/stupside/castor/e2e/strategy"
)

// End is what a live playlist does once every segment is revealed.
type End string

const (
	// Endlist closes the event with EXT-X-ENDLIST, as a live event that finished.
	Endlist End = "endlist"
	// Silence leaves the playlist open and never growing, as an encoder that died.
	Silence End = "silence"
)

// Live builds a live edge: the playlist reveals segments over time, as in `live: {start: 4, every: 1s, end: endlist, window: 6}`.
type Live struct{}

func (Live) Name() string { return "live" }

func (Live) Build(settings yaml.Node) (origin.Behaviour, error) {
	l := live{Start: 4, Every: time.Second, End: Endlist}
	if err := strategy.Decode(settings, &l); err != nil {
		return nil, fmt.Errorf("live: %w", err)
	}
	if l.Start < 1 || l.Every <= 0 || (l.End != Endlist && l.End != Silence) || l.Window < 0 {
		return nil, fmt.Errorf("live: want start >= 1, a positive every, end one of %s, %s, and window 0 (keep every segment) or >= 1", Endlist, Silence)
	}
	return l, nil
}

type live struct {
	Start int           `yaml:"start"`
	Every time.Duration `yaml:"every"`
	End   End           `yaml:"end"`
	// Window is how many of the newest segments the playlist lists and the origin keeps; 0 keeps them all.
	Window int `yaml:"window"`
}

// place is where a segment sits in its media playlist, of how many.
type place struct{ at, of int }

func (live) Name() string { return "live" }
func (live) LiveEdge()    {}

func (l live) Wrap(next http.Handler, p origin.Published) http.Handler {
	var mu sync.Mutex
	places := map[string]place{}
	playlists := serving.RewritesPlaylists(next, func(playlist string) (string, bool) {
		if l.Window > 0 {
			mu.Lock()
			maps.Copy(places, placed(playlist))
			mu.Unlock()
		}
		return l.edge(playlist, l.revealed(p)), true
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l.Window > 0 && p.IsSegment(r) {
			mu.Lock()
			s, listed := places[path.Base(r.URL.Path)]
			mu.Unlock()
			if listed && s.at < min(l.revealed(p), s.of)-l.Window {
				http.Error(w, "segment expired", http.StatusNotFound)
				return
			}
		}
		playlists.ServeHTTP(w, r)
	})
}

func (l live) revealed(p origin.Published) int { return l.Start + int(time.Since(p.Since)/l.Every) }

// placed maps each segment a media playlist lists to its place, so a segment request knows whether it slid out.
func placed(playlist string) map[string]place {
	var uris []string
	for line := range serving.Lines(playlist) {
		if line.Entry() {
			uris = append(uris, path.Base(strings.TrimSpace(line.URI)))
		}
	}
	places := make(map[string]place, len(uris))
	for i, uri := range uris {
		places[uri] = place{at: i, of: len(uris)}
	}
	return places
}

// edge is a media playlist cut to its revealed segments, the newest window of them if any, typed as live; a master passes through.
func (l live) edge(playlist string, revealed int) string {
	var out strings.Builder
	total := strings.Count(playlist, "#EXTINF")
	last := min(revealed, total)
	first := 0
	if l.Window > 0 {
		first = max(0, last-l.Window)
	}
	seen := 0
	for line := range serving.Lines(playlist) {
		switch {
		case strings.HasPrefix(line.Text, "#EXT-X-PLAYLIST-TYPE"), strings.HasPrefix(line.Text, "#EXT-X-ENDLIST"):
		case l.Window > 0 && strings.HasPrefix(line.Text, "#EXT-X-MEDIA-SEQUENCE:"):
			fmt.Fprintf(&out, "#EXT-X-MEDIA-SEQUENCE:%d\n", first)
		case line.Entry():
			if seen >= first && seen < last {
				out.WriteString(line.Text)
				out.WriteString(line.URI)
			}
			seen++
		default:
			if seen == 0 {
				out.WriteString(line.Text)
			}
		}
	}
	if total > 0 && revealed >= total && l.End == Endlist {
		out.WriteString("#EXT-X-ENDLIST\n")
	}
	return out.String()
}
