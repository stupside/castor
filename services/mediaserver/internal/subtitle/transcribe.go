// Package subtitle turns a cast's transcribed speech into the captions burnt into its picture.
package subtitle

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
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

	// promptMaxChars bounds the committed text that restores the context trimmed out of the buffer.
	promptMaxChars = 200
)

// Recognizer turns a window of mono samples at SampleRate into timed words, shifted by offset seconds.
type Recognizer interface {
	Recognize(ctx context.Context, samples []float32, offset float64, prompt string) ([]Word, error)
}

// Transcription commits a PCM feed's words by LocalAgreement-2: a word is committed once two successive hypotheses agree on it.
type Transcription struct {
	mu        sync.Mutex
	done      atomic.Bool // Run has returned; no more words are coming
	latestEnd float64     // end of the last committed word, in seconds
}

// LatestEnd is the end of the last committed word, in seconds.
func (t *Transcription) LatestEnd() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.latestEnd
}

// Done reports whether Run has returned, so no further words will appear.
func (t *Transcription) Done() bool { return t.done.Load() }

func (t *Transcription) setFrontier(sec float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.latestEnd = max(t.latestEnd, sec)
}

func (t *Transcription) markDone() { t.done.Store(true) }

// Run commits pcm's words into sink until pcm ends; open readies the recognizer, and release frees it.
func (t *Transcription) Run(ctx context.Context, pcm io.Reader, open func(context.Context) (r Recognizer, release func(), err error), sink *Cues) error {
	defer t.markDone() // runs last: cues are flushed before Done() flips
	defer sink.Close() // runs first: flush the final pending words

	recognizer, release, err := open(ctx)
	if err != nil {
		return err
	}
	defer release()

	step := make([]byte, stepSeconds*bytesPerSec)
	var (
		buf      []float32 // working audio window
		bufStart float64   // absolute time of buf[0], in seconds
		prev     []Word    // uncommitted tail of the previous hypothesis
		history  []Word    // committed words still inside the buffer
		prompt   string    // committed text already trimmed out of the buffer
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
			buf, bufStart, history, prompt = trimBuffer(ctx, buf, bufStart, history, prompt, frontier)
			// Words of trimmed audio would never be agreed again, and would stall the commit.
			prev = dropCommitted(prev, bufStart)
		}

		var agreed []Word
		// The recognizer needs at least 100ms, which only the final step can lack.
		if len(buf) >= SampleRate/10 {
			agreed, prev = confirm(ctx, recognizer, buf, bufStart, prompt, prev, frontier, atEOF)
			if len(agreed) > 0 {
				frontier = agreed[len(agreed)-1].End
				history = append(history, agreed...)
				t.setFrontier(frontier)
			}
		}

		sink.Commit(agreed, settledTo(frontier, bufStart, buf, prev))

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

func confirm(ctx context.Context, r Recognizer, buf []float32, bufStart float64, prompt string, prev []Word, frontier float64, atEOF bool) (agreed, tail []Word) {
	words, err := r.Recognize(ctx, buf, bufStart, prompt)
	if err != nil {
		slog.WarnContext(ctx, "speech recognition failed", "error", err)
		return nil, prev
	}
	fresh := dropCommitted(words, frontier)
	// The final window has no successor to agree with, so it is committed whole.
	if atEOF {
		return fresh, prev
	}
	agreed = agreedPrefix(prev, fresh)
	return agreed, fresh[len(agreed):]
}

// settledTo is how far the cues are settled: the frontier while a tail is pending, the buffer's end otherwise.
func settledTo(frontier, bufStart float64, buf []float32, tail []Word) float64 {
	if len(tail) == 0 {
		return bufStart + float64(len(buf))/SampleRate
	}
	return frontier
}

// dropCommitted skips the words centred before cutoff, already committed or trimmed.
func dropCommitted(words []Word, cutoff float64) []Word {
	i := 0
	for i < len(words) && (words[i].Start+words[i].End)/2 < cutoff {
		i++
	}
	return words[i:]
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

// trimBuffer drops old audio at a sentence boundary or the hard cap, moving its text into the prompt.
func trimBuffer(ctx context.Context, buf []float32, bufStart float64, history []Word, prompt string, frontier float64) ([]float32, float64, []Word, string) {
	dur := float64(len(buf)) / SampleRate
	if dur <= trimAfterSeconds {
		return buf, bufStart, history, prompt
	}

	var cut float64
	for _, w := range history {
		if sentenceEnd(w.Text) {
			cut = max(cut, w.End)
		}
	}
	if dur > maxBufferSeconds {
		cut = max(cut, frontier)
		if hardMin := bufStart + dur - maxBufferSeconds; cut < hardMin {
			slog.WarnContext(ctx, "dropping unconfirmed audio", "seconds", hardMin-cut)
			cut = hardMin
		}
	}
	if cut <= bufStart {
		return buf, bufStart, history, prompt
	}

	n := min(int((cut-bufStart)*SampleRate), len(buf))
	buf = append(buf[:0], buf[n:]...)
	bufStart += float64(n) / SampleRate

	var b strings.Builder
	b.WriteString(prompt)
	i := 0
	for ; i < len(history) && history[i].End <= bufStart; i++ {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(history[i].Text)
	}
	history = history[i:]
	prompt = b.String()
	if len(prompt) > promptMaxChars {
		cutIdx := len(prompt) - promptMaxChars
		if sp := strings.IndexByte(prompt[cutIdx:], ' '); sp >= 0 {
			cutIdx += sp + 1
		}
		prompt = prompt[cutIdx:]
	}
	return buf, bufStart, history, prompt
}

// appendPCM converts signed 16-bit little-endian PCM samples to float32 in [-1.0, 1.0].
func appendPCM(dst []float32, pcm []byte) []float32 {
	for i := range len(pcm) / 2 {
		s := int16(binary.LittleEndian.Uint16(pcm[i*2:]))
		dst = append(dst, float32(s)/32768.0)
	}
	return dst
}
