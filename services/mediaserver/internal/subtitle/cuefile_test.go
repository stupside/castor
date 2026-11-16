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
