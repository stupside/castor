// Package ffmpeg runs ffmpeg processes with typed outputs and stderr
// forensics. It contains no pipeline policy: arg builders are pure functions,
// and the runner only manages pipes, the stderr tail, and process exit.
package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
)

// stderrTailCapacity bounds the number of recent ffmpeg stderr lines we keep
// for surfacing on failure. ffmpeg emits a startup metadata burst (~20 lines)
// plus one progress line per second; 128 covers the burst plus roughly two
// minutes of progress, so the actual error message survives.
const stderrTailCapacity = 128

// Process is a running ffmpeg invocation.
type Process struct {
	// Stdout is the primary output (pipe:1).
	Stdout io.ReadCloser

	// Extra is the fd-3 output (pipe:3) when started WithExtraPipe;
	// nil otherwise.
	Extra io.ReadCloser

	cmd  *exec.Cmd
	tail *ringTail
}

type startConfig struct {
	stdin     io.Reader
	extraPipe bool
}

type StartOption func(*startConfig)

// WithStdin feeds r to ffmpeg's stdin (pipe:0 input).
func WithStdin(r io.Reader) StartOption {
	return func(c *startConfig) { c.stdin = r }
}

// WithExtraPipe opens a second output pipe on fd 3 (pipe:3), exposed as
// Process.Extra. The arg builder must route an output there.
func WithExtraPipe() StartOption {
	return func(c *startConfig) { c.extraPipe = true }
}

// Start launches ffmpeg at path with args. The process is killed when ctx is
// cancelled.
func Start(ctx context.Context, path string, args []string, opts ...StartOption) (*Process, error) {
	var cfg startConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = cfg.stdin

	var extraRead, extraWrite *os.File
	if cfg.extraPipe {
		var err error
		extraRead, extraWrite, err = os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("extra output pipe: %w", err)
		}
		cmd.ExtraFiles = []*os.File{extraWrite} // fd 3 in the child
	}

	closeExtra := func() {
		if extraRead != nil {
			_ = extraRead.Close()
		}
		if extraWrite != nil {
			_ = extraWrite.Close()
		}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		closeExtra()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		closeExtra()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		closeExtra()
		return nil, fmt.Errorf("starting ffmpeg: %w", err)
	}
	// Close our copy of the write end so Extra sees EOF on ffmpeg exit.
	if extraWrite != nil {
		_ = extraWrite.Close()
	}

	tail := newTail(stderrTailCapacity)
	go drainStderr(ctx, stderr, tail)

	p := &Process{Stdout: stdout, cmd: cmd, tail: tail}
	if extraRead != nil {
		p.Extra = extraRead
	}
	return p, nil
}

// Wait blocks until the process exits and returns its exit error, if any.
// Forensics are the caller's call: use StderrTail or LogStderrTail to
// surface the failure reason when the exit was not self-inflicted.
func (p *Process) Wait() error {
	return p.cmd.Wait()
}

// StderrTail returns the most recent stderr lines, retained even while the
// process is still running. This is what explains a stall after the process
// has been killed by context cancellation — its own error path never runs.
func (p *Process) StderrTail() []string {
	return p.tail.snapshot()
}

// LogStderrTail emits every retained stderr line at WARN under msg.
func (p *Process) LogStderrTail(ctx context.Context, msg string) {
	for _, line := range p.StderrTail() {
		slog.WarnContext(ctx, msg, "line", line)
	}
}

// drainStderr reads stderr line-by-line, logging each at DEBUG and retaining
// the tail for surfacing on failure. ErrClosed is not worth a warning: it only
// means Wait closed the pipe under the scanner during teardown.
func drainStderr(ctx context.Context, r io.Reader, tail *ringTail) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		tail.push(line)
		slog.DebugContext(ctx, "ffmpeg", "line", line)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		slog.WarnContext(ctx, "ffmpeg stderr scanner error", "error", err)
	}
}

// ringTail keeps the most recent N strings pushed into it.
type ringTail struct {
	mu  sync.Mutex
	buf []string
	cap int
}

func newTail(capacity int) *ringTail {
	return &ringTail{buf: make([]string, 0, capacity), cap: capacity}
}

func (t *ringTail) push(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.buf) == t.cap {
		t.buf = t.buf[1:]
	}
	t.buf = append(t.buf, line)
}

func (t *ringTail) snapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.buf))
	copy(out, t.buf)
	return out
}
