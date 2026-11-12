package hls

import (
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

// Castor's own captions add a master naming both renditions, a caption playlist, and one WebVTT file per video fragment.
const (
	masterPlaylist  = "master.m3u8"
	captionPlaylist = "captions.m3u8"
	captionPrefix   = "caption-"
	captionExt      = ".vtt"
	captionNames    = captionPrefix + "%d" + captionExt
)

// captionDelivery writes the caption renditions beside the video playlist, and serves the master, the caption playlist and each fragment's frozen captions.
type captionDelivery struct {
	root        *os.Root
	playlist    string
	cues        *subtitle.Cues
	videoOffset time.Duration

	mu sync.Mutex
	// master and captions are empty until the writer publishes its first fragment.
	master, captions string
	vtt              map[string]string
}

func (c *captionDelivery) serve(w http.ResponseWriter, r *http.Request) bool {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name != masterPlaylist && name != captionPlaylist && !(strings.HasPrefix(name, captionPrefix) && strings.HasSuffix(name, captionExt)) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if vtt, ok := c.vtt[name]; ok {
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		io.WriteString(w, vtt)
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	if c.captions == "" {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "captions are not ready", http.StatusServiceUnavailable)
		return true
	}
	w.Header().Set("Content-Type", media.HLS)
	switch name {
	case masterPlaylist:
		io.WriteString(w, c.master)
	case captionPlaylist:
		io.WriteString(w, c.captions)
	default:
		http.NotFound(w, r)
	}
	return true
}

func captionVTT(cues []subtitle.Cue, origin float64) string {
	var b strings.Builder
	ticks := int64(math.Round(origin * 90000))
	ticks = ((ticks % (1 << 33)) + (1 << 33)) % (1 << 33)
	fmt.Fprintf(&b, "WEBVTT\nX-TIMESTAMP-MAP=LOCAL:00:00:00.000,MPEGTS:%d\n\n", ticks)
	for _, cue := range cues {
		fmt.Fprintf(&b, "%s --> %s\n%s\n\n", captionTime(cue.Start), captionTime(cue.End), html.EscapeString(cue.Text))
	}
	return b.String()
}

func captionTime(seconds float64) string {
	ms := time.Duration(math.Round(max(0, seconds)*1000)) * time.Millisecond
	return fmt.Sprintf("%02d:%02d:%02d.%03d", int64(ms/time.Hour), int64(ms/time.Minute)%60, int64(ms/time.Second)%60, int64(ms/time.Millisecond)%1000)
}
