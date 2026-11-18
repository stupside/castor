package subtitle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
)

func TestAgreedPrefix(t *testing.T) {
	prev := []Word{
		{Start: 1.0, End: 1.3, Text: "Hello,"},
		{Start: 1.4, End: 1.8, Text: "world"},
		{Start: 1.9, End: 2.2, Text: "again"},
	}
	cur := []Word{
		{Start: 1.1, End: 1.4, Text: "hello"}, // case/punct differ, times within tolerance
		{Start: 1.5, End: 1.9, Text: "world"},
		{Start: 2.0, End: 2.3, Text: "against"}, // text mismatch stops the prefix
	}
	got := agreedPrefix(prev, cur)
	if len(got) != 2 {
		t.Fatalf("agreedPrefix = %d words, want 2 (%v)", len(got), got)
	}
	if got[0].Text != "hello" {
		t.Errorf("committed word should carry the current hypothesis' surface form, got %q", got[0].Text)
	}
	if got := agreedPrefix(nil, cur); len(got) != 0 {
		t.Errorf("first hypothesis must commit nothing, got %v", got)
	}
}

func TestDropCommitted(t *testing.T) {
	words := []Word{
		{Start: 0.0, End: 0.5, Text: "old"},
		{Start: 0.6, End: 1.0, Text: "boundary"},
		{Start: 1.2, End: 1.6, Text: "new"},
	}
	got := dropBefore(words, 1.0)
	if len(got) != 1 || got[0].Text != "new" {
		t.Fatalf("dropBefore = %v, want just the word past the frontier", got)
	}
}

type recognitionFunc func(context.Context, []float32, float64) ([]Word, error)

func (f recognitionFunc) Recognize(ctx context.Context, samples []float32, offset float64) ([]Word, error) {
	return f(ctx, samples, offset)
}

func TestTranscribePublishesSilentAudioFrontier(t *testing.T) {
	cues := &Cues{}
	pcm := bytes.NewReader(make([]byte, 7*bytesPerSec))
	r := recognitionFunc(func(context.Context, []float32, float64) ([]Word, error) { return nil, nil })
	if err := Transcribe(t.Context(), pcm, r, cues); err != nil {
		t.Fatal(err)
	}
	got := cues.Snapshot(0, 10)
	if got.SettledTo != 7 || !got.Complete || len(got.Cues) != 0 {
		t.Fatalf("silent transcription = %+v", got)
	}
}

func TestTranscribeDoesNotPublishFailedRecognitionAsSilence(t *testing.T) {
	cues := &Cues{}
	failed := errors.New("recognizer failed")
	r := recognitionFunc(func(context.Context, []float32, float64) ([]Word, error) { return nil, failed })
	if err := Transcribe(t.Context(), bytes.NewReader(make([]byte, 6*bytesPerSec)), r, cues); !errors.Is(err, failed) {
		t.Fatalf("error = %v", err)
	}
	got := cues.Snapshot(0, 10)
	if got.SettledTo != 0 || !got.Complete {
		t.Fatalf("failed transcription = %+v", got)
	}
}

// loudWithSilence is n seconds of steady sound with silence over [from, to).
func loudWithSilence(n int, from, to float64) []float32 {
	buf := make([]float32, n*SampleRate)
	for i := range buf {
		buf[i] = 0.5
	}
	for i := int(from * SampleRate); i < int(to*SampleRate); i++ {
		buf[i] = 0
	}
	return buf
}

func TestQuietestPointIsInsideThePause(t *testing.T) {
	got := quietestPoint(loudWithSilence(6, 3.0, 3.5), 0, 0, 6)
	if got < 3.0 || got > 3.5 {
		t.Fatalf("quietestPoint = %.2f, want inside the silence [3.0, 3.5]", got)
	}
	// A zone too short for the stretch cuts where it ends.
	if got := quietestPoint(loudWithSilence(6, 3.0, 3.5), 0, 5.9, 6); got != 6 {
		t.Fatalf("quietestPoint in a short zone = %.2f, want its end", got)
	}
}

func TestTrimBufferCutsInThePauseOfTheCommittedTail(t *testing.T) {
	buf := loudWithSilence(20, 9.9, 10.5)
	trimmed, start := trimBuffer(buf, 100, 112)
	if start < 109.9 || start > 110.5 {
		t.Fatalf("buffer now starts at %.2f, want inside the pause [109.9, 110.5]", start)
	}
	if want := 20*SampleRate - int(math.Round((start-100)*SampleRate)); len(trimmed) != want {
		t.Fatalf("kept %d samples, want %d", len(trimmed), want)
	}
	if _, start := trimBuffer(loudWithSilence(10, 4, 5), 100, 108); start != 100 {
		t.Fatalf("a buffer under the trim threshold moved to %.2f", start)
	}
}

func TestTrimBufferDropsUnconfirmedAudioOnlyToStayUnderTheCap(t *testing.T) {
	trimmed, start := trimBuffer(loudWithSilence(30, 0, 0), 100, 101)
	if got := float64(len(trimmed)) / SampleRate; got > maxBufferSeconds {
		t.Fatalf("buffer kept %.1fs, over the %ds cap", got, maxBufferSeconds)
	}
	if start < 102 {
		t.Fatalf("buffer starts at %.2f, want at least the 2s the cap needs dropped", start)
	}
}

func TestDropRepeatedSkipsWhatTheCommittedTailAlreadySaid(t *testing.T) {
	committed := []Word{{Text: "the"}, {Text: "oceans,"}, {Text: "Will"}}
	words := []Word{{Text: "oceans"}, {Text: "will"}, {Text: "become"}, {Text: "acidic"}}
	got := dropRepeated(words, committed)
	if len(got) != 2 || got[0].Text != "become" {
		t.Fatalf("dropRepeated = %v, want the words past the repeated 'oceans will'", got)
	}
	if got := dropRepeated(words[2:], committed); len(got) != 2 {
		t.Fatalf("words that repeat nothing were dropped: %v", got)
	}
	if got := dropRepeated(words, nil); len(got) != 4 {
		t.Fatalf("nothing committed yet, yet %v", got)
	}
}

func TestCollapseLoopsKeepsOneOccurrenceOfAHallucinatedLoop(t *testing.T) {
	say := func(phrase string, times int) []Word {
		var words []Word
		for range times {
			for w := range strings.FieldsSeq(phrase) {
				words = append(words, Word{Text: w})
			}
		}
		return words
	}
	loop := append(append(say("Well.", 1), say("Thank you.", 5)...), say("Goodbye.", 1)...)
	if got := joinWords(collapseLoops(loop)); got != "Well. Thank you. Goodbye." {
		t.Fatalf("collapseLoops = %q", got)
	}
	// Below the threshold the speaker may mean it.
	if got := len(collapseLoops(say("no", 3))); got != 3 {
		t.Fatalf("three repeats collapsed to %d words", got)
	}
	// The longest phrase wins, so a repeated sentence is not eaten word by word.
	if got := len(collapseLoops(say("a b c a b c", 4))); got != 3 {
		t.Fatalf("a repeated 3-word phrase left %d words, want one occurrence", got)
	}
}

func TestSilenceNeverFillsTheWindow(t *testing.T) {
	var longest int
	r := recognitionFunc(func(_ context.Context, samples []float32, _ float64) ([]Word, error) {
		longest = max(longest, len(samples))
		return nil, nil
	})
	if err := Transcribe(t.Context(), bytes.NewReader(make([]byte, 60*bytesPerSec)), r, &Cues{}); err != nil {
		t.Fatal(err)
	}
	if got := float64(longest) / SampleRate; got > trimAfterSeconds+stepSeconds {
		t.Fatalf("a silent window grew to %.0fs, want it trimmed past %ds", got, trimAfterSeconds)
	}
}

// A speaker says one word every half second. The recognizer hears each one the same way in every window, except one that
// it hears differently every time, so no two hypotheses ever agree on it.
func TestAWordTheHypothesesNeverAgreeOnDoesNotHoldBackTheWordsAfterIt(t *testing.T) {
	const words, unstable = 240, 130
	calls := 0
	r := recognitionFunc(func(_ context.Context, samples []float32, offset float64) ([]Word, error) {
		calls++
		end := offset + float64(len(samples))/SampleRate
		var heard []Word
		for k := range words {
			start := float64(k) / 2
			if start < offset || start+0.4 > end {
				continue
			}
			text := fmt.Sprintf("w%d", k)
			if k == unstable {
				text = fmt.Sprintf("unsure%d", calls)
			}
			heard = append(heard, Word{Start: start, End: start + 0.4, Text: text})
		}
		return heard, nil
	})
	cues := &Cues{}
	if err := Transcribe(t.Context(), bytes.NewReader(make([]byte, words/2*bytesPerSec)), r, cues); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cues.Snapshot(0, 1e9).Cues {
		for w := range strings.FieldsSeq(c.Text) {
			if strings.HasPrefix(w, "w") {
				got = append(got, w)
			}
		}
	}
	var want []string
	for k := range words {
		if k != unstable {
			want = append(want, fmt.Sprintf("w%d", k))
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("committed %d of the %d steady words, want every one in order; first missing: %v", len(got), len(want), firstMissing(want, got))
	}
}

// firstMissing is the first of want that got lacks.
func firstMissing(want, got []string) string {
	for _, w := range want {
		if !slices.Contains(got, w) {
			return w
		}
	}
	return ""
}

func TestAudioIsConfirmedUpToTheFirstWordStillPending(t *testing.T) {
	buf := make([]float32, 20*SampleRate)
	for _, tt := range []struct {
		name     string
		frontier float64
		pending  []Word
		want     float64
	}{
		{"words pending long after the last one committed", 10, []Word{{Start: 112, End: 112.4, Text: "late"}}, 112},
		{"words pending before the frontier", 114, []Word{{Start: 112, End: 112.4, Text: "late"}}, 114},
		{"nothing pending: all but the step just read", 10, nil, 117},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := (&window{samples: buf, start: 100}).confirmedTo(tt.frontier, tt.pending); got != tt.want {
				t.Errorf("confirmedTo = %.1f, want %.1f", got, tt.want)
			}
		})
	}
}

// The recognizer gives "king" again, later than the frontier the first time it was committed at, so only what was said
// is left to tell it is the same word.
func TestAWordTheLastWindowCommittedIsNotCommittedAgainWhateverItsTimes(t *testing.T) {
	var done committed
	done.add([]Word{{Start: 1, End: 1.4, Text: "the"}, {Start: 1.5, End: 1.9, Text: "king"}})
	r := recognitionFunc(func(context.Context, []float32, float64) ([]Word, error) {
		return []Word{{Start: 2.1, End: 2.5, Text: "King,"}, {Start: 2.6, End: 3.0, Text: "rode"}}, nil
	})
	_, pending, err := confirm(t.Context(), r, window{samples: make([]float32, 4*SampleRate)}, nil, done, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Text != "rode" {
		t.Fatalf("pending = %v, want just the new word", pending)
	}
	if done.frontier != 1.9 || len(done.tail) != 2 {
		t.Fatalf("committed ends at %.1f with %d words kept, want 1.9 and 2", done.frontier, len(done.tail))
	}
}
