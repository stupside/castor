package progressive

import "net/http"

// resumeLive returns only immutable absolute offsets that are still retained.
// A fresh byte-zero request cannot be silently restarted mid-container.
func (s *Stream) resumeLive(header string) served {
	sp := s.run.Load().spool
	first, end := sp.Retained()
	total, closed := sp.Snapshot()
	if !closed {
		total = -1
	}
	result := served{code: http.StatusOK, start: 0, end: -1, total: total}
	start, last, ok := byteRange(header)
	// The whole stream, following production: asked so, asked of a delivery declining ranges, or from byte zero before the end is known.
	if header == "" || s.rangesDeclined() || ok && start == 0 && last < 0 && !closed {
		if first > 0 {
			result.code = http.StatusGone
		}
		return result
	}
	if !ok || start >= end {
		return served{code: http.StatusRequestedRangeNotSatisfiable, total: total}
	}
	if start < first {
		return served{code: http.StatusGone, total: total}
	}
	if last < 0 || last >= end {
		last = end - 1
	}
	return served{code: http.StatusPartialContent, start: start, end: last, total: total}
}
