package whisper

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stupside/castor/internal/cast/cue"
)

func TestAgreedPrefix(t *testing.T) {
	prev := []word{
		{Start: 1.0, End: 1.3, Text: "Hello,"},
		{Start: 1.4, End: 1.8, Text: "world"},
		{Start: 1.9, End: 2.2, Text: "again"},
	}
	cur := []word{
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

func TestSameWordTextMatch(t *testing.T) {
	tests := []struct {
		a, b word
		want bool
	}{
		{word{Text: "Hello,"}, word{Text: "hello"}, true},         // punctuation + case
		{word{Text: "world"}, word{Text: "world"}, true},          // exact
		{word{Text: "again"}, word{Text: "against"}, false},       // different words
		{word{Text: "[Music]"}, word{Text: "[Music]"}, true},      // noise matches itself
		{word{Text: "hello"}, word{Text: "hello!"}, true},        // trailing punct
	}
	for _, tt := range tests {
		got := sameWord(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("sameWord(%q, %q) = %v, want %v", tt.a.Text, tt.b.Text, got, tt.want)
		}
	}
}

func TestDropCommitted(t *testing.T) {
	words := []word{
		{Start: 0.0, End: 0.5, Text: "old"},
		{Start: 0.6, End: 1.0, Text: "boundary"},
		{Start: 1.2, End: 1.6, Text: "new"},
	}
	got := dropCommitted(words, 1.0)
	if len(got) != 1 || got[0].Text != "new" {
		t.Fatalf("dropCommitted = %v, want just the word past the frontier", got)
	}
}

func TestTrimBufferMovesTextToPrompt(t *testing.T) {
	ctx := context.Background()
	buf := make([]float32, 20*SampleRate) // 20s, past the soft threshold
	history := []word{
		{Start: 2.0, End: 2.5, Text: "Sentence"},
		{Start: 2.6, End: 3.0, Text: "one."},
		{Start: 4.0, End: 4.5, Text: "Then"},
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
	if len(history) != 1 || history[0].Text != "Then" {
		t.Errorf("history should keep in-buffer words, got %v", history)
	}
}

// TestStreamingSmoke runs the full LocalAgreement loop over whisper.cpp's
// bundled JFK sample into a cue.Builder. It needs the real models, so it skips
// unless the default whisper model is already in the user cache (the VAD model
// is small enough to fetch on first run).
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
	builder := cue.NewBuilder()
	if err := tr.Run(ctx, bytes.NewReader(wav[44:]), builder); err != nil { // 44-byte canonical WAV header
		t.Fatal(err)
	}
	if !tr.Done() {
		t.Fatal("transcriber not done after Run")
	}

	cues := builder.Cues()
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
