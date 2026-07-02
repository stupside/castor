package whisper

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgreedPrefix(t *testing.T) {
	prev := []word{
		{start: 1.0, end: 1.3, text: "Hello,"},
		{start: 1.4, end: 1.8, text: "world"},
		{start: 1.9, end: 2.2, text: "again"},
	}
	cur := []word{
		{start: 1.1, end: 1.4, text: "hello"}, // case/punct differ, times within tolerance
		{start: 1.5, end: 1.9, text: "world"},
		{start: 2.0, end: 2.3, text: "against"}, // text mismatch stops the prefix
	}
	got := agreedPrefix(prev, cur)
	if len(got) != 2 {
		t.Fatalf("agreedPrefix = %d words, want 2 (%v)", len(got), got)
	}
	if got[0].text != "hello" {
		t.Errorf("committed word should carry the current hypothesis' surface form, got %q", got[0].text)
	}
	if got := agreedPrefix(nil, cur); len(got) != 0 {
		t.Errorf("first hypothesis must commit nothing, got %v", got)
	}
}

func TestSameWordTimeTolerance(t *testing.T) {
	a := word{start: 1.0, end: 1.3, text: "word"}
	b := word{start: 2.5, end: 2.8, text: "word"}
	if sameWord(a, b) {
		t.Error("words drifting past the tolerance must not agree")
	}
}

func TestDropCommitted(t *testing.T) {
	words := []word{
		{start: 0.0, end: 0.5, text: "old"},
		{start: 0.6, end: 1.0, text: "boundary"},
		{start: 1.2, end: 1.6, text: "new"},
	}
	got := dropCommitted(words, 1.0)
	if len(got) != 1 || got[0].text != "new" {
		t.Fatalf("dropCommitted = %v, want just the word past the frontier", got)
	}
}

func TestCueCut(t *testing.T) {
	sentence := []word{
		{start: 0.0, end: 0.3, text: "Ask"},
		{start: 0.4, end: 0.6, text: "not."},
		{start: 0.7, end: 1.0, text: "What"},
	}
	if cut := cueCut(sentence); cut != 2 {
		t.Errorf("sentence-final punctuation should close after 2 words, got %d", cut)
	}

	gap := []word{
		{start: 0.0, end: 0.3, text: "before"},
		{start: 2.0, end: 2.3, text: "after"},
	}
	if cut := cueCut(gap); cut != 1 {
		t.Errorf("a silence gap should close before it, got %d", cut)
	}

	open := []word{{start: 0.0, end: 0.3, text: "still"}, {start: 0.4, end: 0.7, text: "going"}}
	if cut := cueCut(open); cut != 0 {
		t.Errorf("no closing signal should leave the cue open, got %d", cut)
	}
}

func TestCloseCuesOrdering(t *testing.T) {
	tr := &Transcriber{}
	pending := []word{
		{start: 0.0, end: 0.4, text: "First"},
		{start: 0.5, end: 0.9, text: "line."},
		{start: 3.0, end: 3.4, text: "Second"},
		{start: 3.5, end: 3.9, text: "line."},
	}
	rest := tr.closeCues(pending, false)
	if len(rest) != 0 {
		t.Fatalf("both sentences should close, %d words left", len(rest))
	}
	if n := len(tr.cues); n != 2 {
		t.Fatalf("want 2 cues, got %d: %v", n, tr.cues)
	}
	if tr.cues[0].Start > tr.cues[1].Start {
		t.Error("cues must be appended in non-decreasing start order")
	}
	if tr.CueAt(3.2) != "Second line." {
		t.Errorf("CueAt(3.2) = %q", tr.CueAt(3.2))
	}
}

func TestTrimBufferMovesTextToPrompt(t *testing.T) {
	ctx := context.Background()
	buf := make([]float32, 20*SampleRate) // 20s, past the soft threshold
	history := []word{
		{start: 2.0, end: 2.5, text: "Sentence"},
		{start: 2.6, end: 3.0, text: "one."},
		{start: 4.0, end: 4.5, text: "Then"},
	}
	buf, bufStart, history, prompt := trimBuffer(ctx, buf, 0, history, "", 4.5)
	if bufStart != 3.0 {
		t.Fatalf("buffer should be cut at the sentence end, start = %v", bufStart)
	}
	if wantLen := 17 * SampleRate; len(buf) != wantLen {
		t.Errorf("buffer length = %d, want %d", len(buf), wantLen)
	}
	if prompt != "Sentence one." {
		t.Errorf("prompt = %q", prompt)
	}
	if len(history) != 1 || history[0].text != "Then" {
		t.Errorf("history should keep in-buffer words, got %v", history)
	}
}

// TestStreamingSmoke runs the full LocalAgreement loop over whisper.cpp's
// bundled JFK sample. It needs the real models, so it skips unless the
// default whisper model is already in the user cache (the VAD model is small
// enough to fetch on first run).
func TestStreamingSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skip("no user cache dir")
	}
	if _, err := os.Stat(filepath.Join(cache, "castor", "whisper", defaultModelName)); err != nil {
		t.Skip("whisper model not cached; run a cast once first")
	}
	wav, err := os.ReadFile("../../../third_party/whisper.cpp/samples/jfk.wav")
	if err != nil {
		t.Skipf("sample audio unavailable: %v", err)
	}

	ctx := context.Background()
	tr, err := New(ctx, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Run(ctx, bytes.NewReader(wav[44:])); err != nil { // 44-byte canonical WAV header
		t.Fatal(err)
	}
	if !tr.Done() {
		t.Fatal("transcriber not done after Run")
	}

	tr.mu.Lock()
	cues := append([]Cue(nil), tr.cues...)
	tr.mu.Unlock()
	if len(cues) == 0 {
		t.Fatal("no cues committed")
	}
	var all []string
	for _, c := range cues {
		t.Logf("cue %5.2f–%5.2f %q", c.Start, c.End, c.Text)
		if c.End <= c.Start {
			t.Errorf("cue has non-positive duration: %+v", c)
		}
		all = append(all, c.Text)
	}
	if joined := strings.ToLower(strings.Join(all, " ")); !strings.Contains(joined, "country") {
		t.Errorf("transcript missing expected content: %q", joined)
	}
}
