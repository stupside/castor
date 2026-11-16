package subtitle

import (
	"slices"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

const (
	cueGapSeconds = 1.0
	cueMinSeconds = 1.2
	cueMaxSeconds = 6.0

	cuePauseSeconds = 0.4

	cueStartTrim = 0.15
	cueEndTrim   = 0.20
	minCueSpan   = 0.30
)

// Word is one committed, timed token with absolute timestamps in seconds.
type Word struct {
	Start, End float64
	Text       string
}

// Cue is one immutable subtitle line with absolute timestamps in seconds.
type Cue struct {
	Start, End float64
	Text       string
}

// Snapshot contains committed cues overlapping a requested time range. SettledTo
// is the time before which no further cues can start, including confirmed silence.
// Complete means the producer has returned and no further cues will arrive.
type Snapshot struct {
	Cues      []Cue
	SettledTo float64
	Complete  bool
}

// Cues folds a stream of committed words into display cues.
type Cues struct {
	published     atomic.Pointer[Snapshot]
	discardBefore atomic.Pointer[float64]
	settledTo     float64

	pending []Word // committed words not yet closed into a cue (Commit-only)
}

// Commit folds newly committed words into cues.
func (c *Cues) Commit(words []Word, settledTo float64) {
	// Published cue timelines cannot be revised by a later hypothesis.
	frontier := 0.0
	if current := c.published.Load(); current != nil {
		frontier = current.SettledTo
	}
	for _, word := range words {
		if word.End <= frontier {
			continue
		}
		word.Start = max(word.Start, frontier)
		c.pending = append(c.pending, word)
	}
	silentTail := len(c.pending) > 0 &&
		settledTo-c.pending[len(c.pending)-1].End >= cueGapSeconds
	c.settledTo = max(c.settledTo, settledTo)
	var committed []Cue
	c.pending, committed = closeCues(c.pending, c.settledTo, silentTail)
	c.publish(committed, false)
}

// Close flushes every remaining word into cues. Call it once, after the final Commit.
func (c *Cues) Close() {
	remaining, committed := closeCues(c.pending, c.settledTo, true)
	c.pending = remaining
	c.publish(committed, true)
}

// Snapshot returns a detached copy safe for the caller to retain or modify.
func (c *Cues) Snapshot(start, end float64) Snapshot {
	current := c.published.Load()
	if current == nil {
		return Snapshot{}
	}
	result := Snapshot{SettledTo: current.SettledTo, Complete: current.Complete}
	for _, cue := range current.Cues {
		if cue.Start >= end {
			break
		}
		if cue.End > start {
			result.Cues = append(result.Cues, cue)
		}
	}
	return result
}

// DiscardBefore releases consumed captions immediately, including after the
// producer has finished. Published snapshots are the sole committed storage.
func (c *Cues) DiscardBefore(seconds float64) {
	c.discardBefore.Store(&seconds)
	for {
		current := c.published.Load()
		if current == nil {
			return
		}
		cut := 0
		for cut < len(current.Cues) && current.Cues[cut].End <= seconds {
			cut++
		}
		if cut == 0 {
			return
		}
		next := &Snapshot{Cues: slices.Clone(current.Cues[cut:]), SettledTo: current.SettledTo, Complete: current.Complete}
		if c.published.CompareAndSwap(current, next) {
			return
		}
	}
}

// Only the producer adds cues. A concurrent consumer may prune the snapshot, so
// retry against that newer view rather than restoring any discarded prefix.
func (c *Cues) publish(committed []Cue, complete bool) {
	frontier := c.settledTo
	if len(c.pending) > 0 {
		frontier = min(frontier, c.pending[0].Start)
	}
	for {
		current := c.published.Load()
		var cues []Cue
		if current != nil {
			cues = current.Cues
		}
		cues = append(cues, committed...)
		if before := c.discardBefore.Load(); before != nil {
			cut := 0
			for cut < len(cues) && cues[cut].End <= *before {
				cut++
			}
			if cut > 0 {
				cues = slices.Clone(cues[cut:])
			}
		}
		if c.published.CompareAndSwap(current, &Snapshot{Cues: cues, SettledTo: frontier, Complete: complete}) {
			return
		}
	}
}

// cueAt returns the text of the cue covering time tSec, or "" if none does.
func (c *Cues) cueAt(tSec float64) string {
	current := c.published.Load()
	if current == nil {
		return ""
	}
	i, _ := slices.BinarySearchFunc(current.Cues, tSec, func(entry Cue, t float64) int {
		if entry.Start > t {
			return 1
		}
		return -1
	})
	if i > 0 && current.Cues[i-1].End > tSec {
		return current.Cues[i-1].Text
	}
	return ""
}

// closeCues cuts the words that form complete cues; a cue may stay on screen until the next word or settled.
func closeCues(pending []Word, settled float64, final bool) ([]Word, []Cue) {
	var committed []Cue
	for len(pending) > 0 {
		cut := cueCut(pending)
		if cut == 0 {
			if !final {
				break
			}
			cut = len(pending)
		}
		limit := settled
		if cut < len(pending) {
			limit = pending[cut].Start
		}
		committed = append(committed, makeCue(pending[:cut], limit))
		pending = pending[cut:]
	}
	return pending, committed
}

// makeCue ends the cue late enough to read at cueColumnsPerSecond, but never past limit.
func makeCue(words []Word, limit float64) Cue {
	text := cueText(words)
	start, end := trimCueEdges(words[0].Start, words[len(words)-1].End)
	end = max(end, min(start+float64(columns(text))/cueColumnsPerSecond, limit))
	return Cue{Start: start, End: end, Text: text}
}

func cueCut(pending []Word) int {
	lastBreak := 0 // words up to the most recent clause break or pause
	for i, w := range pending {
		if i > 0 && w.Start-pending[i-1].End >= cueGapSeconds {
			return i
		}
		fits := fitsLines(pending[:i+1])
		span := w.End - pending[0].Start

		// Over budget: end the line at a natural boundary, not mid-phrase.
		if (!fits || span >= cueMaxSeconds) && i > 0 {
			if fits && clauseEnd(w.Text) {
				return i + 1
			}
			if lastBreak > 0 {
				return lastBreak
			}
			if !fits {
				return i
			}
			return i + 1
		}

		if sentenceEnd(w.Text) && span >= cueMinSeconds {
			return i + 1
		}

		// Remember natural boundaries to fall back on if a later word forces a cut.
		if clauseEnd(w.Text) ||
			(i+1 < len(pending) && pending[i+1].Start-w.End >= cuePauseSeconds) {
			lastBreak = i + 1
		}
	}
	return 0
}

// trimCueEdges counters a recognizer's habit of over-reporting word spans.
func trimCueEdges(start, end float64) (float64, float64) {
	if end-start < cueStartTrim+cueEndTrim+minCueSpan {
		return start, end
	}
	return start + cueStartTrim, end - cueEndTrim
}

// sentenceEnd reports whether a word closes a sentence, ignoring trailing quotes and brackets.
func sentenceEnd(s string) bool {
	s = strings.TrimRight(s, `"')]`+"”’")
	r, _ := utf8.DecodeLastRuneInString(s)
	return strings.ContainsRune(".?!…。！？", r)
}

// clauseEnd reports whether a word ends a clause, ignoring trailing quotes and brackets.
func clauseEnd(s string) bool {
	s = strings.TrimRight(s, `"')]`+"”’")
	r, _ := utf8.DecodeLastRuneInString(s)
	return strings.ContainsRune(".?!…,;:—。！？，、；：", r)
}
