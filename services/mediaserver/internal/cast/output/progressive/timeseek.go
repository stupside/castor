package progressive

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// timeSeekHeader is how a DLNA renderer asks for a stream from a position in time.
const timeSeekHeader = "TimeSeekRange.dlna.org"

// seekTo has the stream begin again at the position asked, for this request and every one after it.
func (s *Stream) seekTo(ctx context.Context, w http.ResponseWriter, r *http.Request, asked string) (served, bool) {
	at, ok := nptStart(asked)
	slog.InfoContext(ctx, "stream seek", "from", r.RemoteAddr, "user_agent", r.UserAgent(), "asked", asked, "at", at)
	switch {
	case s.seek == nil:
		http.Error(w, "this stream is served from its start only", http.StatusNotAcceptable)
		return served{}, false
	case !ok || at >= s.runtime:
		http.Error(w, "the position asked for is not in the stream", http.StatusRequestedRangeNotSatisfiable)
		return served{}, false
	}
	if err := s.seek(ctx, at); err != nil {
		slog.WarnContext(ctx, "the stream could not be made from the position asked for", "at", at, "error", err)
		http.Error(w, "the stream could not be made from there", http.StatusServiceUnavailable)
		return served{}, false
	}
	// The stream begins at the keyframe the source has there, which the answer states.
	w.Header().Set(timeSeekHeader, "npt="+nptSeconds(s.Began())+"-"+nptSeconds(s.runtime)+"/"+nptSeconds(s.runtime))
	return served{code: http.StatusOK, start: 0, end: -1, total: -1}, true
}

// nptStart is the start of a TimeSeekRange npt range, in seconds (12.5) or as H:MM:SS(.f).
func nptStart(header string) (time.Duration, bool) {
	spec, found := strings.CutPrefix(strings.TrimSpace(header), "npt=")
	start, _, ranged := strings.Cut(spec, "-")
	if !found || !ranged {
		return 0, false
	}
	parts := strings.Split(strings.TrimSpace(start), ":")
	if len(parts) > 3 {
		return 0, false
	}
	var seconds float64
	for _, part := range parts {
		v, err := strconv.ParseFloat(part, 64)
		if err != nil || v < 0 || math.IsInf(v, 0) || math.IsNaN(v) {
			return 0, false
		}
		seconds = seconds*60 + v
	}
	// Anything past a year is no position in a cast, and would overflow a duration.
	if seconds > (365 * 24 * time.Hour).Seconds() {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func nptSeconds(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) }
