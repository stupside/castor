package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
)

// stderrTailCapacity bounds the retained complaints; only the last few ever reach a message.
const stderrTailCapacity = 32

type Evidence struct {
	// ExitStatus is the process exit code (noExitStatus = not reaped or castor killed it).
	ExitStatus int

	// Signaled is a signal castor did not send ending it; ffmpeg's own exit codes pass 128 too.
	Signaled bool

	// Lines are the most recent complaints on stderr (warning and worse), kept even while the process runs.
	Lines []string
}

// isComplaint is a line ffmpeg flagged warning or worse; -loglevel level puts the tag after any "[context @ address] ".
func isComplaint(line string) bool {
	return slices.ContainsFunc([]string{"[warning] ", "[error] ", "[fatal] ", "[panic] "}, func(tag string) bool {
		return strings.Contains(line, tag)
	})
}

// Evidence collects what the process left behind (read after Wait for complete status).
func (p *Process) Evidence() Evidence {
	return Evidence{
		ExitStatus: int(p.status.Load()),
		Signaled:   p.signaled.Load(),
		Lines:      p.tail.snapshot(),
	}
}

// stderrLineBuffer bounds one line read; Scanner would abandon over-long lines (blocks ffmpeg).
const stderrLineBuffer = 16 << 10

// drainStderr reads stderr line by line into the tail and the marker watch; it must read to EOF.
func drainStderr(ctx context.Context, r io.Reader, tail *ringTail, markers *markerWatch) {
	buffered := bufio.NewReaderSize(r, stderrLineBuffer)
	for {
		chunk, err := buffered.ReadSlice('\n')
		if line := strings.TrimRight(string(chunk), "\r\n"); line != "" {
			tail.Observe(line)
			markers.Observe(line)
			if isComplaint(line) {
				slog.DebugContext(ctx, "ffmpeg", "line", line)
			}
		}
		switch {
		case err == nil, errors.Is(err, bufio.ErrBufferFull):
			// ErrBufferFull: over-long line continues; marker straddled is lost but pipe keeps moving.
			continue
		case errors.Is(err, io.EOF), errors.Is(err, os.ErrClosed):
			return
		default:
			slog.WarnContext(ctx, "ffmpeg stderr read error", "error", err)
			return
		}
	}
}

// ringTail keeps the most recent complaint lines (the marker watch is separate and sees every line).
type ringTail struct {
	mu  sync.Mutex
	buf []string
}

func newRingTail() *ringTail {
	return &ringTail{buf: make([]string, 0, stderrTailCapacity)}
}

func (t *ringTail) Observe(line string) {
	if !isComplaint(line) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.buf) == stderrTailCapacity {
		t.buf = t.buf[1:]
	}
	t.buf = append(t.buf, line)
}

func (t *ringTail) snapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.buf)
}
