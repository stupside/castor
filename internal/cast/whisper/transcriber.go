// Package whisper runs the whisper.cpp Go bindings against a PCM audio
// stream to produce a growing list of subtitle cues. The audio arrives on an
// io.Reader (16kHz mono s16le — the caller owns whatever process produces it)
// and is transcribed with the LocalAgreement-2 streaming policy (Macháček et
// al. 2023, "Turning Whisper into a Real-Time Transcription System"): the
// tail of the stream is re-transcribed on every step and only the word prefix
// that two consecutive hypotheses agree on is committed. Committed words never
// change, so cues can be burned into frames the moment they are emitted.
// Silero VAD (built into whisper.cpp) gates the decoder: silence and music
// never reach the model, which kills both the hallucinated "[Music]"-style
// loops and the mistimed segments that fixed-window chunking suffers at
// window edges.
package whisper

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	wcpp "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

const (
	// SampleRate is the PCM sample rate whisper expects. The audio fed to
	// Run must be mono s16le at this rate.
	SampleRate  = 16000
	bytesPerSec = SampleRate * 2 // mono, s16le

	// stepSeconds is how much new audio each iteration waits for before
	// re-transcribing the buffer. Smaller steps commit words sooner but run
	// whisper — whose per-call cost is dominated by its fixed-size encode —
	// more often per second of audio.
	stepSeconds = 3

	// trimAfterSeconds is the buffer length past which committed audio is
	// trimmed away at a sentence boundary; maxBufferSeconds is the hard cap
	// (whisper's native window is 30s — audio beyond it is invisible to the
	// model anyway, minus slack for VAD padding).
	trimAfterSeconds = 15
	maxBufferSeconds = 28

	// agreeStartTolerance is how far two hypotheses' start times for the
	// same word may drift while still counting as agreement. Whisper times
	// jitter a few hundred ms between runs with different right-context.
	agreeStartTolerance = 1.0

	// Cue shaping: a cue closes before a silence gap of cueGapSeconds or a
	// character-budget overflow, and after sentence-final punctuation or
	// cueMaxSeconds of accumulated duration.
	cueGapSeconds = 1.0
	cueMaxSeconds = 6.0
	cueMaxChars   = 84 // two 42-column broadcast lines

	// promptMaxChars caps the committed-text tail passed to whisper as the
	// initial prompt, restoring the left context that buffer trimming
	// removed from the audio.
	promptMaxChars = 200
)

// Cue is one subtitle line with absolute timestamps in seconds.
type Cue struct {
	Start float64
	End   float64
	Text  string
}

// word is one whisper-emitted word with absolute timestamps in seconds.
type word struct {
	start, end float64
	text       string
}

// Transcriber owns a whisper model and accumulates cues from a PCM stream.
// It is not reusable; call New for each cast.
type Transcriber struct {
	cfg          Config
	modelPath    string
	vadModelPath string

	mu        sync.Mutex
	cues      []Cue
	latestEnd float64 // end of the last committed word, in seconds
	done      bool    // Run has returned; no more cues are coming
}

// New returns a configured Transcriber, resolving the transcription and VAD
// model paths (auto-downloading the defaults into the user cache when unset).
func New(ctx context.Context, cfg Config) (*Transcriber, error) {
	modelPath, err := EnsureModel(ctx, cfg.ModelPath)
	if err != nil {
		return nil, err
	}
	vadModelPath, err := EnsureVADModel(ctx)
	if err != nil {
		return nil, err
	}

	return &Transcriber{
		cfg:          cfg,
		modelPath:    modelPath,
		vadModelPath: vadModelPath,
	}, nil
}

// LatestEnd returns the end-time, in seconds, of the last committed word —
// how far transcription has irrevocably reached.
func (t *Transcriber) LatestEnd() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.latestEnd
}

func (t *Transcriber) setFrontier(sec float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.latestEnd = max(t.latestEnd, sec)
}

func (t *Transcriber) cueCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.cues)
}

// Done reports whether Run has returned, i.e. no further cues will appear.
func (t *Transcriber) Done() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done
}

// CueAt returns the text of the cue covering time tSec, or "" if none does.
func (t *Transcriber) CueAt(tSec float64) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Words commit in time order, so cues are start-ordered and overlap at
	// most by the few ms whisper lets adjacent words share: only the last
	// cue starting at or before tSec can cover it.
	i := sort.Search(len(t.cues), func(i int) bool { return t.cues[i].Start > tSec })
	if i > 0 && t.cues[i-1].End > tSec {
		return t.cues[i-1].Text
	}
	return ""
}

// Run loads the whisper model and consumes pcm (16kHz mono s16le) until EOF
// or ctx cancellation, running the LocalAgreement-2 loop: append a step of
// audio, re-transcribe the working buffer, commit the word prefix confirmed
// by two consecutive hypotheses, fold committed words into cues, and trim
// the buffer behind the commit frontier. It always marks the transcriber
// done on return.
func (t *Transcriber) Run(ctx context.Context, pcm io.Reader) error {
	defer func() {
		t.mu.Lock()
		t.done = true
		t.mu.Unlock()
	}()

	slog.InfoContext(ctx, "loading whisper model", "path", t.modelPath, "vad", t.vadModelPath)
	model, err := wcpp.New(t.modelPath)
	if err != nil {
		return fmt.Errorf("loading whisper model: %w", err)
	}
	defer model.Close()

	step := make([]byte, stepSeconds*bytesPerSec)
	var (
		buf      []float32 // working audio window
		bufStart float64   // absolute time of buf[0], in seconds
		prev     []word    // uncommitted tail of the previous hypothesis
		pending  []word    // committed words not yet closed into cues
		history  []word    // committed words still inside the buffer
		prompt   string    // committed text already trimmed out of the buffer
		frontier float64   // absolute end time of the last committed word
	)
	lastProgress := time.Now()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		n, readErr := io.ReadFull(pcm, step)
		atEOF := readErr != nil && (errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF))
		if readErr != nil && !atEOF {
			return fmt.Errorf("reading pcm stream: %w", readErr)
		}
		buf = appendPCM(buf, step[:n])

		// whisper rejects windows under 100ms; with less than that at EOF
		// there is nothing left to transcribe.
		if len(buf) >= SampleRate/10 {
			words, err := t.transcribeBuffer(ctx, model, buf, bufStart, prompt)
			if err != nil {
				slog.WarnContext(ctx, "whisper inference failed", "error", err)
			} else {
				fresh := dropCommitted(words, frontier)
				agreed := fresh
				if !atEOF {
					// LocalAgreement-2: commit the prefix two consecutive
					// hypotheses agree on. The final window has no successor
					// to agree with, so everything in it is committed.
					agreed = agreedPrefix(prev, fresh)
					prev = fresh[len(agreed):]
				}
				if len(agreed) > 0 {
					frontier = agreed[len(agreed)-1].end
					pending = append(pending, agreed...)
					history = append(history, agreed...)
					t.setFrontier(frontier)
				}
			}
		}

		// A paragraph-final cue has no successor word to reveal its closing
		// gap; once the audio has run past it by the gap with nothing new
		// pending, the silence is confirmed and the cue must not wait.
		silentTail := len(prev) == 0 && len(pending) > 0 &&
			bufStart+float64(len(buf))/SampleRate-pending[len(pending)-1].end >= cueGapSeconds
		pending = t.closeCues(pending, atEOF || silentTail)

		if atEOF {
			slog.InfoContext(ctx, "transcription finished", "transcribed_seconds", int(frontier))
			return nil
		}

		buf, bufStart, history, prompt = trimBuffer(ctx, buf, bufStart, history, prompt, frontier)
		// Words from a hypothesis whose audio was trimmed away can never be
		// re-confirmed; drop them so agreement doesn't stall on ghosts.
		prev = dropCommitted(prev, bufStart)

		if time.Since(lastProgress) >= 15*time.Second {
			slog.InfoContext(ctx, "transcription progress",
				"committed_seconds", int(frontier),
				"buffered_seconds", int(float64(len(buf))/SampleRate),
				"cue_count", t.cueCount(),
			)
			lastProgress = time.Now()
		}
	}
}

// transcribeBuffer runs whisper over the working buffer and returns one word
// per emitted segment, time-shifted by offset (the buffer's absolute start).
// Each call gets a fresh context: the bindings default to no_context=true,
// which resets the model's rolling text state, so the only conditioning is
// the explicit prompt.
func (t *Transcriber) transcribeBuffer(ctx context.Context, model wcpp.Model, samples []float32, offset float64, prompt string) ([]word, error) {
	wctx, err := model.NewContext()
	if err != nil {
		return nil, fmt.Errorf("new whisper context: %w", err)
	}

	// Pin the language when configured; per-buffer auto-detection misfires
	// on music and quiet stretches. English-only models need no pinning.
	if t.cfg.Language != "" && t.cfg.Language != "auto" && wctx.IsMultilingual() {
		if err := wctx.SetLanguage(t.cfg.Language); err != nil {
			slog.WarnContext(ctx, "whisper SetLanguage failed", "language", t.cfg.Language, "error", err)
		}
	}

	// Silero VAD gates the decoder; whisper.cpp maps the resulting segment
	// timestamps back onto the unfiltered timeline, so offsets stay valid.
	wctx.SetVAD(true)
	wctx.SetVADModelPath(t.vadModelPath)

	// One word per segment: LocalAgreement compares hypotheses word by
	// word, and cue shaping regroups the committed words into lines.
	wctx.SetTokenTimestamps(true)
	wctx.SetSplitOnWord(true)
	wctx.SetMaxSegmentLength(1)

	if prompt != "" {
		wctx.SetInitialPrompt(prompt)
	}

	if err := wctx.Process(samples, nil, nil, nil); err != nil {
		return nil, fmt.Errorf("whisper process: %w", err)
	}

	var words []word
	for {
		seg, err := wctx.NextSegment()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("whisper segment: %w", err)
		}
		if isNoise(seg.Text) {
			continue
		}
		words = append(words, word{
			start: seg.Start.Seconds() + offset,
			end:   seg.End.Seconds() + offset,
			text:  seg.Text,
		})
	}
	return words, nil
}

// dropCommitted skips the words whose midpoint lies before cutoff — audio a
// previous iteration already committed (with jittered timestamps), or audio
// that has been trimmed out of the buffer.
func dropCommitted(words []word, cutoff float64) []word {
	i := 0
	for i < len(words) && (words[i].start+words[i].end)/2 < cutoff {
		i++
	}
	return words[i:]
}

// agreedPrefix returns the longest prefix on which the previous and current
// hypotheses agree. The current hypothesis' words are the ones returned:
// transcribed with more right-context, their timestamps are the fresher of
// the two.
func agreedPrefix(prev, cur []word) []word {
	i := 0
	for i < min(len(prev), len(cur)) && sameWord(prev[i], cur[i]) {
		i++
	}
	return cur[:i]
}

func sameWord(a, b word) bool {
	if math.Abs(a.start-b.start) > agreeStartTolerance {
		return false
	}
	na, nb := normalizeWord(a.text), normalizeWord(b.text)
	if na == "" && nb == "" {
		return a.text == b.text
	}
	return na == nb
}

// normalizeWord strips case and edge punctuation so cosmetic differences
// between hypotheses ("Hello," vs "hello") don't block agreement.
func normalizeWord(s string) string {
	return strings.TrimFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// isNoise reports whether a word is a non-speech annotation ("[Music]",
// "(applause)", "♪") — the residue VAD lets through on borderline audio.
func isNoise(s string) bool {
	if s == "" {
		return true
	}
	return s[0] == '[' || s[0] == '(' || strings.HasPrefix(s, "♪")
}

// closeCues folds committed words into display cues, appending every cue
// that has a closing signal. When final is set the remainder is flushed
// unconditionally. It returns the words still waiting to close.
func (t *Transcriber) closeCues(pending []word, final bool) []word {
	for len(pending) > 0 {
		cut := cueCut(pending)
		if cut == 0 {
			if !final {
				break
			}
			cut = len(pending)
		}
		t.appendCue(pending[:cut])
		pending = pending[cut:]
	}
	return pending
}

// cueCut returns how many leading words of pending form a complete cue, or 0
// if no closing signal has arrived yet.
func cueCut(pending []word) int {
	chars := 0
	for i, w := range pending {
		if i > 0 && w.start-pending[i-1].end >= cueGapSeconds {
			return i
		}
		chars += len(w.text)
		if i > 0 {
			chars++ // joining space
		}
		if chars > cueMaxChars && i > 0 {
			return i
		}
		if sentenceEnd(w.text) || w.end-pending[0].start >= cueMaxSeconds {
			return i + 1
		}
	}
	return 0
}

// sentenceEnd reports whether a word closes a sentence, ignoring trailing
// quotes and brackets.
func sentenceEnd(s string) bool {
	s = strings.TrimRight(s, `"')]`+"”’")
	r, _ := utf8.DecodeLastRuneInString(s)
	return strings.ContainsRune(".?!…", r)
}

func (t *Transcriber) appendCue(words []word) {
	var b strings.Builder
	for i, w := range words {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(w.text)
	}
	cue := Cue{Start: words[0].start, End: words[len(words)-1].end, Text: b.String()}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.cues = append(t.cues, cue)
}

// trimBuffer drops audio the streaming loop is finished with. Past
// trimAfterSeconds the buffer is cut at the last committed sentence end, so
// the decoder keeps seeing whole utterances; past maxBufferSeconds it is cut
// at the commit frontier regardless (audio beyond whisper's native window is
// invisible to the model anyway). Text trimmed out of the buffer moves into
// the prompt so the decoder keeps its left context.
func trimBuffer(ctx context.Context, buf []float32, bufStart float64, history []word, prompt string, frontier float64) ([]float32, float64, []word, string) {
	dur := float64(len(buf)) / SampleRate
	if dur <= trimAfterSeconds {
		return buf, bufStart, history, prompt
	}

	var cut float64
	for _, w := range history {
		if sentenceEnd(w.text) {
			cut = max(cut, w.end)
		}
	}
	if dur > maxBufferSeconds {
		cut = max(cut, frontier)
		if hardMin := bufStart + dur - maxBufferSeconds; cut < hardMin {
			// Nothing committed in over a whole window — hypotheses that
			// never agree (usually music VAD half-passes). Keep the window
			// legal and accept that the oldest unconfirmed audio is lost.
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

	// Fold the words that left the buffer into the prompt tail.
	var b strings.Builder
	b.WriteString(prompt)
	i := 0
	for ; i < len(history) && history[i].end <= bufStart; i++ {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(history[i].text)
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

// appendPCM converts signed 16-bit little-endian PCM samples to float32 in
// [-1.0, 1.0], appending to dst.
func appendPCM(dst []float32, pcm []byte) []float32 {
	for i := range len(pcm) / 2 {
		s := int16(binary.LittleEndian.Uint16(pcm[i*2:]))
		dst = append(dst, float32(s)/32768.0)
	}
	return dst
}
