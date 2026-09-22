// Package ffmpeg runs ffmpeg processes with typed outputs and stderr
// forensics. It contains no pipeline policy: arg builders are pure functions,
// and the runner only manages pipes, the stderr tail, and process exit.
package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
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

	cmd  *exec.Cmd
	tail *ringTail

	// extra is the second output this process was started WithExtraOutput,
	// kept only so Wait can end it (see Wait).
	extra *ExtraOutput
}

type startConfig struct {
	stdin   io.Reader
	extra   *ExtraOutput
	workDir string
}

type StartOption func(*startConfig)

// WithStdin feeds r to ffmpeg's stdin (pipe:0 input).
func WithStdin(r io.Reader) StartOption {
	return func(c *startConfig) { c.stdin = r }
}

// WithWorkDir runs ffmpeg with dir as its working directory, so a muxer writing
// relative output files (the HLS playlist and segments) lands them there.
func WithWorkDir(dir string) StartOption {
	return func(c *startConfig) { c.workDir = dir }
}

// WithExtraOutput ties o to the process, whose arg builder must route an
// output to o.URL(): a failed Start closes o, and Wait releases a reader
// ffmpeg never connected to. The caller keeps reading o itself.
func WithExtraOutput(o *ExtraOutput) StartOption {
	return func(c *startConfig) { c.extra = o }
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
	cmd.Dir = cfg.workDir

	closeExtra := func() {
		if cfg.extra != nil {
			_ = cfg.extra.Close()
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
	tail := newTail(stderrTailCapacity)
	go drainStderr(ctx, stderr, tail)

	return &Process{Stdout: stdout, cmd: cmd, tail: tail, extra: cfg.extra}, nil
}

// Wait blocks until the process exits and returns its exit error, if any.
// Forensics are the caller's call: use StderrTail or LogStderrTail to
// surface the failure reason when the exit was not self-inflicted.
func (p *Process) Wait() error {
	err := p.cmd.Wait()
	// An ffmpeg that died before opening its extra output never will: release
	// the reader waiting for it, which then sees EOF.
	if p.extra != nil {
		p.extra.stopAccepting()
	}
	return err
}

// Kill signals the process to stop immediately. It is idempotent and safe to
// call after the process has already exited (the error is ignored), so callers
// can defer it as unconditional teardown on paths where context cancellation is
// not the only way the encoder must stop (the HLS serve path, whose output is
// files rather than a pipe that would EPIPE on close). Reap it with Wait.
func (p *Process) Kill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
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
// the tail for surfacing on failure.
func drainStderr(ctx context.Context, r io.Reader, tail *ringTail) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		tail.push(line)
		slog.DebugContext(ctx, "ffmpeg", "line", line)
	}
	if err := scanner.Err(); err != nil {
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
