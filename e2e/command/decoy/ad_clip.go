// Package decoy is what pages request beside their stream: the ads, posters, blobs and empty manifests castor must ignore.
package decoy

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/stupside/castor/e2e/command"
	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/origin/serving"
)

// clipSegments is how much of the stream the ad replays: a few seconds, the length of a pre-roll.
const clipSegments = 3

// AdClip is a pre-roll: a closed playlist of the stream's first seconds, real media too short to be the content.
type AdClip struct{}

func (AdClip) Name() string { return "ad-clip" }

func (AdClip) Mount(mux *http.ServeMux, src *origin.Origin) string {
	mux.HandleFunc("/ad/preroll.m3u8", func(w http.ResponseWriter, r *http.Request) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, src.URL, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		clip, ok := cut(string(body), src.URL)
		if !ok {
			http.Error(w, "the stream is no media playlist an ad can be cut from", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-mpegURL")
		_, _ = io.WriteString(w, clip)
	})
	return command.Fetch("/ad/preroll.m3u8")
}

// cut is the playlist's header and first segments, closed, with every URI made absolute against the stream's.
func cut(playlist, base string) (string, bool) {
	root, err := url.Parse(base)
	if err != nil {
		return "", false
	}
	var out strings.Builder
	segments := 0
	for line := range serving.Lines(playlist) {
		if segments == clipSegments {
			break
		}
		switch {
		case line.Entry():
			uri, err := root.Parse(strings.TrimSpace(line.URI))
			if err != nil {
				return "", false
			}
			out.WriteString(line.Text)
			out.WriteString(uri.String())
			out.WriteString("\n")
			segments++
		case strings.HasPrefix(line.Text, "#EXT-X-ENDLIST"), strings.HasPrefix(line.Text, "#EXT-X-STREAM-INF"):
		default:
			out.WriteString(line.Text)
		}
	}
	return out.String() + "#EXT-X-ENDLIST\n", segments > 0
}
