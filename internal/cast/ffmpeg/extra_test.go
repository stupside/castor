package ffmpeg

import (
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func lookFFmpeg(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH; skipping the extra output test (it drives a real ffmpeg)")
	}
	return path
}

// runExtra starts ffmpeg with args built around the extra output's URL, reads
// the extra output to EOF, and reaps the process.
func runExtra(t *testing.T, args func(url string) []string) (string, error) {
	t.Helper()
	out, err := NewExtraOutput()
	if err != nil {
		t.Fatal(err)
	}
	proc, err := Start(t.Context(), lookFFmpeg(t), args(out.URL()), WithExtraOutput(out))
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(out)
		got <- b
	}()
	_, _ = io.Copy(io.Discard, proc.Stdout)
	waitErr := proc.Wait()
	select {
	case b := <-got:
		return string(b), waitErr
	case <-time.After(10 * time.Second):
		t.Fatal("extra output never reached EOF after ffmpeg exited")
		return "", nil
	}
}

func TestExtraOutputCarriesPCM(t *testing.T) {
	pcm, err := runExtra(t, func(url string) []string {
		return []string{"-hide_banner", "-nostats", "-f", "lavfi", "-i", "sine=d=1",
			"-ac", "1", "-ar", "16000", "-f", "s16le", url}
	})
	if err != nil {
		t.Fatalf("ffmpeg: %v", err)
	}
	// One second of mono s16le at 16kHz, whole: a dropped or truncated tail
	// is whisper losing the end of the cast.
	if len(pcm) != 32000 {
		t.Errorf("got %d PCM bytes, want 32000", len(pcm))
	}
}

func TestExtraOutputCarriesProgress(t *testing.T) {
	feed, err := runExtra(t, func(url string) []string {
		return []string{"-hide_banner", "-nostats", "-f", "lavfi", "-i", "testsrc=d=1",
			"-progress", url, "-f", "null", "-"}
	})
	if err != nil {
		t.Fatalf("ffmpeg: %v", err)
	}
	if !strings.Contains(feed, "progress=end") {
		t.Errorf("progress feed has no final block:\n%s", feed)
	}
}

// An ffmpeg that dies before opening its outputs must not leave the reader
// parked on a connection that never comes.
func TestExtraOutputEndsWhenFFmpegNeverConnects(t *testing.T) {
	feed, err := runExtra(t, func(url string) []string {
		return []string{"-hide_banner", "-i", "/nonexistent/castor-input", "-f", "s16le", url}
	})
	if err == nil {
		t.Fatal("ffmpeg succeeded on a missing input")
	}
	if feed != "" {
		t.Errorf("got %q from an output ffmpeg never opened", feed)
	}
}
