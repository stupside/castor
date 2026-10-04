package subtitle

import (
	"os"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestTheCueFileHoldsTheLineForTheFrameBeingEncoded(t *testing.T) {
	file, path := cueFixture(t)
	write := file.Writer(t.Context())

	// Mux position 1.5s, so the frame being drawn is around 2.5s, inside the committed cue.
	write(media.Progress{Position: 1500 * time.Millisecond, Speed: 1.15})
	if got := readFile(t, path); got != "Hello." {
		t.Errorf("cue file = %q, want the line covering the frame being encoded", got)
	}

	// Past the cue, the file must go empty rather than keep the last line on screen.
	write(media.Progress{Position: 5 * time.Second, Speed: 1.15})
	if got := readFile(t, path); got != "" {
		t.Errorf("cue file = %q after the cue ended, want it cleared", got)
	}
}

func cueFixture(t *testing.T) (*CueFile, string) {
	t.Helper()
	cues := &Cues{}
	cues.Commit([]Word{{Start: 2, End: 4, Text: "Hello."}}, 10)
	file := NewCueFile(t.TempDir(), cues)
	path, err := file.Create()
	if err != nil {
		t.Fatal(err)
	}
	return file, path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWrap(t *testing.T) {
	for _, tt := range []struct {
		name  string
		in    string
		width int
		want  string
	}{
		// Never inside a word: a split word is unreadable at television distance.
		{"a tight width breaks between words", "alpha beta gamma", 10, "alpha beta\ngamma"},
		{"a word longer than the width is left whole", "supercalifragilistic", 10, "supercalifragilistic"},
		// Columns, not bytes.
		{"an accented line gets the full width", "\u00e9t\u00e9 \u00e9t\u00e9 \u00e9t\u00e9", 8, "\u00e9t\u00e9 \u00e9t\u00e9\n\u00e9t\u00e9"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := wrap(tt.in, tt.width); got != tt.want {
				t.Errorf("wrap(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
			}
		})
	}
}
