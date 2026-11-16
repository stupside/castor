package whisper

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

func TestWorkerHelper(t *testing.T) {
	mode := os.Getenv("TEST_CASTOR_WORKER")
	if mode == "" {
		return
	}
	for {
		var req workerRequest
		if err := readFrame(os.Stdin, &req); err != nil {
			os.Exit(0)
		}
		switch mode {
		case "hang":
			time.Sleep(time.Hour)
		case "crash":
			_, _ = io.WriteString(os.Stderr, "native failure")
			os.Exit(7)
		default:
			if err := writeFrame(os.Stdout, workerResponse{Words: []subtitle.Word{{Text: "hello", Start: req.Offset, End: req.Offset + 1}}}); err != nil {
				os.Exit(8)
			}
		}
	}
}
func stubWorker(mode string) *workerRecognizer {
	r := newWorker(workerConfig{})
	r.timeout = 2 * time.Second
	r.start = func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerHelper$")
		cmd.Env = append(os.Environ(), "TEST_CASTOR_WORKER="+mode)
		return cmd
	}
	return r
}
func TestWorkerRoundTripAndRecycling(t *testing.T) {
	r := stubWorker("answer")
	defer r.Close()
	starts := 0
	start := r.start
	r.start = func() *exec.Cmd { starts++; return start() }
	for range workerWindows + 1 {
		words, err := r.Recognize(t.Context(), []float32{0}, 4)
		if err != nil {
			t.Fatal(err)
		}
		if len(words) != 1 || words[0].Start != 4 {
			t.Fatalf("words=%v", words)
		}
	}
	if starts != 2 {
		t.Fatalf("worker starts=%d, want recycling after %d requests", starts, workerWindows)
	}

}
func TestWorkerDeadlineAndCrashAreBounded(t *testing.T) {
	for _, mode := range []string{"hang", "crash"} {
		t.Run(mode, func(t *testing.T) {
			r := stubWorker(mode)
			r.timeout = 100 * time.Millisecond
			defer r.Close()
			start := time.Now()
			_, err := r.Recognize(t.Context(), []float32{0}, 0)
			if err == nil {
				t.Fatal("worker failure lost")
			}
			if mode == "crash" && !strings.Contains(err.Error(), "native failure") {
				t.Fatalf("worker diagnostic lost: %v", err)
			}
			if mode == "hang" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if time.Since(start) > 3*time.Second || r.process != nil {
				t.Fatal("native work outlived request")
			}
		})
	}
}
func TestRequestCancellationReapsInFlightInference(t *testing.T) {
	r := stubWorker("hang")
	defer r.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := r.Recognize(ctx, []float32{0}, 0); done <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("request cancellation=%v", err)
	}
	if r.process != nil {
		t.Fatal("native process retained after canceled request")
	}
}
func TestWorkerFramesAndRequestsAreBounded(t *testing.T) {
	var frame bytes.Buffer
	_ = binary.Write(&frame, binary.BigEndian, uint32(workerFrameBytes+1))
	if err := readFrame(&frame, &workerRequest{}); err == nil {
		t.Fatal("oversize frame accepted")
	}
	if err := readFrame(strings.NewReader("\x00\x00\x00\x05x"), &workerRequest{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	if err := (workerRequest{ModelPath: strings.Repeat("x", 8193)}).validate(); err == nil {
		t.Fatal("unbounded model path accepted")
	}
}
