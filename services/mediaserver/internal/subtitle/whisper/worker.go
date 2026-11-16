package whisper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

const inferenceTimeout = 60 * time.Second

// workerRecognizer belongs to one transcription, which recognizes serially and
// closes it after the final request. Context cancellation interrupts in-flight
// inference; workerProcess owns and reaps subprocess resources.
type workerRecognizer struct {
	config  workerConfig
	start   func() *exec.Cmd
	timeout time.Duration
	process *workerProcess
}

func newWorker(config workerConfig) *workerRecognizer {
	return &workerRecognizer{config: config, timeout: inferenceTimeout, start: func() *exec.Cmd {
		executable, err := os.Executable()
		if err != nil {
			executable = os.Args[0]
		}
		return exec.Command(executable, workerArgument)
	}}
}

func (r *workerRecognizer) Close() {
	if r.process != nil {
		r.process.stop()
		r.process = nil
	}
}

func (r *workerRecognizer) Recognize(ctx context.Context, samples []float32, offset float64) ([]subtitle.Word, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	req := workerRequest{workerConfig: r.config, Samples: samples, Offset: offset}
	if err := req.validate(); err != nil {
		return nil, err
	}
	if r.process == nil {
		process, err := startWorkerProcess(r.start())
		if err != nil {
			return nil, fmt.Errorf("starting native worker: %w", err)
		}
		r.process = process
	}
	requestCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	response, err := r.process.exchange(requestCtx, req)
	if err != nil {
		r.Close()
		return nil, err
	}
	if r.process.completed >= workerWindows {
		r.Close()
	}
	if response.Error != "" {
		r.Close()
		return nil, errors.New(response.Error)
	}
	return response.Words, nil
}
