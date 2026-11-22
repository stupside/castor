package progressive

import (
	"net/http"
	"strconv"
	"strings"
)

// served is the stretch of the stream one response carries, and the status that states it.
type served struct {
	// code is 200 (the whole stream from byte 0), 206 or 416 (a start past a known end).
	code int
	// start is the first byte this response carries, end the last one inclusive.
	start int64
	end   int64
	total int64
}

func (s *Stream) resume(rangeHeader string) (served, string) {
	if s.run.Load().spool.Live() {
		return s.resumeLive(rangeHeader), ""
	}
	whole := served{code: http.StatusOK, start: 0, end: -1, total: -1}
	if rangeHeader == "" {
		return whole, ""
	}
	start, end, ok := byteRange(rangeHeader)
	if !ok {
		return whole, "the Range header is not a single bytes=first-last range"
	}
	if start == 0 && end < 0 {
		// The ordinary replay: the client is asking for exactly the bytes it is about to be given.
		return whole, ""
	}
	refuse := func(why string) (served, string) {
		if start == 0 {
			return whole, ""
		}
		return whole, why
	}
	if s.rangesDeclined() {
		return refuse("this delivery's own response headers declare Accept-Ranges: none")
	}
	total, final := s.snapshot()
	switch {
	case !final:
		return refuse("the producer is still running, and a player takes a 206 ending at the bytes made so far for the stream's end")
	case start >= total:
		return served{code: http.StatusRequestedRangeNotSatisfiable, total: total}, ""
	case end >= 0:
		return served{code: http.StatusPartialContent, start: start, end: min(end, total-1), total: total}, ""
	default:
		return served{code: http.StatusPartialContent, start: start, end: total - 1, total: total}, ""
	}
}

// byteRange parses the one form a resume takes: a single "bytes=first-last", with the last byte optional.
func byteRange(header string) (start, end int64, ok bool) {
	spec, found := strings.CutPrefix(strings.TrimSpace(header), "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false
	}
	first, last, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(first), 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false
	}
	if strings.TrimSpace(last) == "" {
		return start, -1, true
	}
	end, err = strconv.ParseInt(strings.TrimSpace(last), 10, 64)
	if err != nil || end < start {
		return 0, 0, false
	}
	return start, end, true
}

func (s *Stream) rangesDeclined() bool {
	for k, v := range s.o.Headers {
		if http.CanonicalHeaderKey(k) == "Accept-Ranges" {
			return strings.EqualFold(strings.TrimSpace(v), "none")
		}
	}
	return false
}
