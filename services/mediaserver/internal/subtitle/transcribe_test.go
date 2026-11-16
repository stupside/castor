package subtitle

import (
	"bytes"
	"context"
	"errors"
	"math"
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
	trimmed, start := trimBuffer(t.Context(), buf, 100, 112)
	if start < 109.9 || start > 110.5 {
		t.Fatalf("buffer now starts at %.2f, want inside the pause [109.9, 110.5]", start)
	}
	if want := 20*SampleRate - int(math.Round((start-100)*SampleRate)); len(trimmed) != want {
		t.Fatalf("kept %d samples, want %d", len(trimmed), want)
	}
	if _, start := trimBuffer(t.Context(), loudWithSilence(10, 4, 5), 100, 108); start != 100 {
		t.Fatalf("a buffer under the trim threshold moved to %.2f", start)
	}
}

func TestTrimBufferDropsUnconfirmedAudioOnlyToStayUnderTheCap(t *testing.T) {
	trimmed, start := trimBuffer(t.Context(), loudWithSilence(30, 0, 0), 100, 101)
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
