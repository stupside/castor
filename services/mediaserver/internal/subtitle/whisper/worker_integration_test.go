//go:build nativeintegration

package whisper

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestNativeWorkerRealRecognition(t *testing.T) {
	model, vad := os.Getenv("TEST_CASTOR_WHISPER_MODEL"), os.Getenv("TEST_CASTOR_WHISPER_VAD")
	if model == "" || vad == "" {
		t.Fatal("set TEST_CASTOR_WHISPER_MODEL and TEST_CASTOR_WHISPER_VAD to existing model files for native integration")
	}
	pcm, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-i", "../../../../../third_party/whisper.cpp/samples/jfk.wav", "-f", "f32le", "-ar", "16000", "-ac", "1", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	samples := make([]float32, len(pcm)/4)
	if err := binary.Read(bytes.NewReader(pcm), binary.LittleEndian, samples); err != nil {
		t.Fatal(err)
	}
	windows := 2
	if raw := os.Getenv("TEST_CASTOR_WHISPER_WINDOWS"); raw != "" {
		windows, err = strconv.Atoi(raw)
		if err != nil || windows < 1 || windows > 256 {
			t.Fatal("TEST_CASTOR_WHISPER_WINDOWS must be between 1 and 256")
		}
	}
	for range 2 {
		t.Run("isolated-model", func(t *testing.T) {
			t.Parallel()
			r := newWorker(workerConfig{ModelPath: model, VADPath: vad, Language: "en"})
			defer r.Close()
			for window := range windows {
				words, err := r.Recognize(t.Context(), samples, 0)
				if err != nil {
					t.Fatal(err)
				}
				var text strings.Builder
				for _, word := range words {
					text.WriteString(word.Text)
				}
				if !strings.Contains(strings.ToLower(text.String()), "country") {
					t.Fatalf("unexpected recognition: %q", text.String())
				}
				if (window+1)%workerWindows == 0 && r.process != nil {
					t.Fatal("native worker was not recycled at its window limit")
				}
				if (window+1)%16 == 0 || window+1 == windows {
					t.Logf("completed %d/%d native windows", window+1, windows)
				}
			}
		})
	}
}
