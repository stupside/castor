// Package subtitle turns transcribed speech into timed captions for native tracks or burn-in.
package subtitle

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
)

// SampleRate is the rate the PCM feed a transcription reads must be produced at: mono s16le at 16 kHz.
const SampleRate = 16000

const (
	// bytesPerSec is the PCM feed's rate in bytes (mono s16le).
	bytesPerSec = SampleRate * 2

	// stepSeconds trades committing sooner against running the recognizer more often.
	stepSeconds = 3

	// The buffer is trimmed at a sentence boundary past the first, and capped short of whisper's 30s window.
	trimAfterSeconds = 15
	maxBufferSeconds = 28

	// A cut searches this much committed audio for its quietest stretch.
	cutZoneSeconds  = 6
	cutQuietSeconds = 0.2

	// repeatedWordsMax bounds the committed tail a later window may repeat.
	repeatedWordsMax = 5

	// A phrase of up to loopPhraseMax words said loopMinTimes times in a row is a recognizer loop.
	loopPhraseMax = 4
	loopMinTimes  = 4
)

// Recognizer turns a window of mono samples at SampleRate into timed words, shifted by offset seconds.
type Recognizer interface {
	Recognize(ctx context.Context, samples []float32, offset float64) ([]Word, error)
}

// Transcribe commits a PCM feed's words by LocalAgreement-2: a word is committed
// once two successive hypotheses agree on it. The caller owns the recognizer.
func Transcribe(ctx context.Context, pcm io.Reader, recognizer Recognizer, sink *Cues) error {
	defer sink.Close()
	step := make([]byte, stepSeconds*bytesPerSec)
	var (
		buf      []float32 // working audio window
		bufStart float64   // absolute time of buf[0], in seconds
		prev     []Word    // uncommitted tail of the previous hypothesis
		tail     []Word    // last committed words, kept across trims to drop what a window repeats
		frontier float64   // absolute end time of the last committed word
	)
	lastProgress := time.Now()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		n, atEOF, err := readStep(pcm, step)
		if err != nil {
			return err
		}
		buf = appendPCM(buf, step[:n])
		if !atEOF {
			// Trimmed before recognizing, so the window never outgrows the recognizer's.
			buf, bufStart = trimBuffer(ctx, buf, bufStart, confirmedTo(frontier, bufStart, buf, prev))
			// Words of trimmed audio would never be agreed again, and would stall the commit.
			prev = dropBefore(prev, bufStart)
		}

		var agreed []Word
		// The recognizer needs at least 100ms, which only the final step can lack.
		if len(buf) >= SampleRate/10 {
			agreed, prev, err = confirm(ctx, recognizer, buf, bufStart, prev, tail, frontier, atEOF)
			if err != nil {
				return err
			}
			if len(agreed) > 0 {
				frontier = agreed[len(agreed)-1].End
				tail = append(tail, agreed...)
				tail = tail[max(0, len(tail)-repeatedWordsMax):]
			}
		}

		settled := settledTo(frontier, bufStart, buf, prev)
		if !atEOF && len(prev) == 0 {
			// Silence also needs a successor window before it becomes immutable.
			settled = max(frontier, settled-stepSeconds)
		}
		sink.Commit(agreed, settled)

		if atEOF {
			slog.InfoContext(ctx, "transcription finished", "transcribed_seconds", int(frontier))
			return nil
		}

		if time.Since(lastProgress) >= 15*time.Second {
			slog.InfoContext(ctx, "transcription progress",
				"committed_seconds", int(frontier),
				"buffered_seconds", int(float64(len(buf))/SampleRate),
			)
			lastProgress = time.Now()
		}
	}
}

func readStep(pcm io.Reader, step []byte) (int, bool, error) {
	n, err := io.ReadFull(pcm, step)
	atEOF := err != nil && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF))
	if err != nil && !atEOF {
		return n, false, fmt.Errorf("reading pcm stream: %w", err)
	}
	return n, atEOF, nil
}

func confirm(ctx context.Context, r Recognizer, buf []float32, bufStart float64, prev, committed []Word, frontier float64, atEOF bool) (agreed, tail []Word, err error) {
	words, err := r.Recognize(ctx, buf, bufStart)
	if err != nil {
		return nil, prev, fmt.Errorf("recognizing speech: %w", err)
	}
	fresh := dropRepeated(dropBefore(collapseLoops(words), frontier), committed)
	slog.DebugContext(ctx, "recognized a window", "from", bufStart, "heard", len(words), "new", len(fresh))
	// The final window has no successor to agree with, so it is committed whole.
	if atEOF {
		return fresh, nil, nil
	}
	agreed = agreedPrefix(prev, fresh)
	return agreed, fresh[len(agreed):], nil
}

// confirmedTo is how far the audio holds nothing still to commit: the frontier while a tail is pending,
// else everything the last window heard, which leaves out the step just read.
func confirmedTo(frontier, bufStart float64, buf []float32, tail []Word) float64 {
	if len(tail) > 0 {
		return frontier
	}
	return max(frontier, bufStart+float64(len(buf))/SampleRate-stepSeconds)
}

// settledTo is how far the cues are settled: the frontier while a tail is pending, the buffer's end otherwise.
func settledTo(frontier, bufStart float64, buf []float32, tail []Word) float64 {
	if len(tail) == 0 {
		return bufStart + float64(len(buf))/SampleRate
	}
	return frontier
}

// dropBefore skips the words centred before cutoff, already committed or trimmed.
func dropBefore(words []Word, cutoff float64) []Word {
	i := 0
	for i < len(words) && (words[i].Start+words[i].End)/2 < cutoff {
		i++
	}
	return words[i:]
}

// collapseLoops keeps one occurrence of a phrase the recognizer repeats in a hallucinated loop, the longest phrases first.
func collapseLoops(words []Word) []Word {
	for n := loopPhraseMax; n >= 1; n-- {
		var kept []Word
		for i := 0; i < len(words); {
			times := 1
			for i+(times+1)*n <= len(words) && slices.EqualFunc(words[i:i+n], words[i+times*n:i+(times+1)*n], sameWord) {
				times++
			}
			if times >= loopMinTimes {
				kept = append(kept, words[i:i+n]...)
				i += times * n
				continue
			}
			kept = append(kept, words[i])
			i++
		}
		words = kept
	}
	return words
}

// dropRepeated skips the leading words that restate the committed tail, whatever their timestamps say.
func dropRepeated(words, committed []Word) []Word {
	for n := min(len(committed), len(words), repeatedWordsMax); n >= 1; n-- {
		if slices.EqualFunc(committed[len(committed)-n:], words[:n], sameWord) {
			return words[n:]
		}
	}
	return words
}

// agreedPrefix is the longest prefix both hypotheses agree on, in the current one's words for their fresher timestamps.
func agreedPrefix(prev, cur []Word) []Word {
	i := 0
	for i < min(len(prev), len(cur)) && sameWord(prev[i], cur[i]) {
		i++
	}
	return cur[:i]
}

func sameWord(a, b Word) bool {
	na, nb := normalizeWord(a.Text), normalizeWord(b.Text)
	if na == "" && nb == "" {
		return a.Text == b.Text
	}
	return na == nb
}

// normalizeWord strips case and edge punctuation, which hypotheses vary on cosmetically.
func normalizeWord(s string) string {
	return strings.TrimFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// trimBuffer drops old audio at the quietest point of what is confirmed.
func trimBuffer(ctx context.Context, buf []float32, bufStart, confirmed float64) ([]float32, float64) {
	dur := float64(len(buf)) / SampleRate
	if dur <= trimAfterSeconds {
		return buf, bufStart
	}

	// Word timestamps run up to a second off, so the cut is placed by the audio's own energy.
	lo := max(bufStart, bufStart+dur-maxBufferSeconds)
	hi := min(confirmed, bufStart+dur)
	if hi < lo {
		slog.WarnContext(ctx, "dropping unconfirmed audio", "seconds", lo-hi)
		hi = lo
	}
	cut := quietestPoint(buf, bufStart, max(lo, hi-cutZoneSeconds), hi)
	if cut <= bufStart {
		return buf, bufStart
	}

	n := min(int(math.Round((cut-bufStart)*SampleRate)), len(buf))
	buf = append(buf[:0], buf[n:]...)
	return buf, bufStart + float64(n)/SampleRate
}

// quietestPoint is the centre of the least energetic cutQuietSeconds stretch within [from, to], the latest on a tie.
func quietestPoint(buf []float32, bufStart, from, to float64) float64 {
	first := max(0, int(math.Round((from-bufStart)*SampleRate)))
	last := min(len(buf), int(math.Round((to-bufStart)*SampleRate)))
	width := int(cutQuietSeconds * SampleRate)
	if last-first < width {
		return to
	}
	var sum float64
	for _, s := range buf[first : first+width] {
		sum += float64(s) * float64(s)
	}
	quietest, quietestAt := sum, first
	for i := first + 1; i+width <= last; i++ {
		out, in := float64(buf[i-1]), float64(buf[i+width-1])
		sum += in*in - out*out
		if sum <= quietest {
			quietest, quietestAt = sum, i
		}
	}
	return bufStart + float64(quietestAt+width/2)/SampleRate
}

// appendPCM converts signed 16-bit little-endian PCM samples to float32 in [-1.0, 1.0].
func appendPCM(dst []float32, pcm []byte) []float32 {
	for i := range len(pcm) / 2 {
		s := int16(binary.LittleEndian.Uint16(pcm[i*2:]))
		dst = append(dst, float32(s)/32768.0)
	}
	return dst
}
