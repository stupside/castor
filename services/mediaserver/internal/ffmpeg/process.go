// Package ffmpeg runs the ffmpeg tools: their processes, what they report, and the flags every reader opens a source with.
package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// noExitStatus = no status yet (not waited) or castor's kill ended it (stall: ffmpeg's error path never ran).
const noExitStatus = -1

// signalExitBase is added to signal numbers per shell convention (driver segfault or OOM killer).
const signalExitBase = 128

type Process struct {
	// Stdout is the primary output (pipe:1).
	Stdout io.ReadCloser

	cmd   *exec.Cmd
	input *observedInput

	// extra are the side outputs in pipe order, starting at firstExtraFD.
	extra []*sideOutput

	tail    *ringTail
	markers *markerWatch

	// status is the exit code, and signaled a signal castor did not send; atomics avoid racing ProcessState.
	status   atomic.Int64
	signaled atomic.Bool

	// stopped records that castor sent its kill signal (see exitStatus).
	stopped *atomic.Bool

	// sample is the latest -progress block, stored by the progress drain goroutine.
	sample atomic.Pointer[media.Progress]

	// drained is closed once the progress feed is read to EOF.
	drained chan struct{}

	// scanned is closed once stderr is read to EOF.
	scanned chan struct{}

	// teed is closed once the PCM feed is copied to EOF (at once when there is none).
	teed chan struct{}
}

// Options is what a process runs with beyond its command line; each zero value means none.
type Options struct {
	Stdin io.Reader
	// WorkDir is where relative output files (HLS) are written.
	WorkDir string
	// Progress is called once per -progress sample.
	Progress func(media.Progress)
	// PCM receives the command's PCM tee until ffmpeg closes it; Wait joins the copy, the writer stays the caller's.
	PCM io.Writer
}

// Start launches the command at path. The process is killed when ctx is cancelled.
func Start(ctx context.Context, path string, command Command, opts Options) (p *Process, err error) {
	// An unread tee blocks ffmpeg once the pipe fills, and a writer with no tee waits forever.
	if tees := command.ExtraPipes > pcmFD-firstExtraFD; tees != (opts.PCM != nil) {
		return nil, fmt.Errorf("a PCM tee routed (%t) and a PCM consumer given (%t) disagree", tees, opts.PCM != nil)
	}

	cmd := exec.CommandContext(ctx, path, slices.Clone(command.Args)...)
	var (
		extra                   []*sideOutput
		stdout                  io.ReadCloser
		stderrRead, stderrWrite *os.File
	)
	// Start owns every endpoint until launch succeeds. Parent write handles
	// always close; the process and its drainers take the read ends on success.
	defer func() {
		for _, file := range cmd.ExtraFiles {
			_ = file.Close()
		}
		if stderrWrite != nil {
			_ = stderrWrite.Close()
		}
		if err != nil {
			for _, output := range extra {
				_ = output.Close()
			}
			if stdout != nil {
				_ = stdout.Close()
			}
			if stderrRead != nil {
				_ = stderrRead.Close()
			}
		}
	}()
	for i := range command.ExtraPipes {
		output, err := newSideOutput(cmd, firstExtraFD+i)
		if err != nil {
			return nil, fmt.Errorf("side output: %w", err)
		}
		extra = append(extra, output)
	}

	stopped, scanned := new(atomic.Bool), make(chan struct{})
	cmd.Cancel = sync.OnceValue(func() error { return kill(cmd.Process, scanned, stopped) })
	var input *observedInput
	if opts.Stdin != nil {
		input = &observedInput{Reader: opts.Stdin}
		cmd.Stdin = input
	}
	cmd.Dir = opts.WorkDir

	stdout, err = cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// Our own pipe: os/exec closes it at Wait, losing buffered tail (ffmpeg's error path).
	stderrRead, stderrWrite, err = os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	cmd.Stderr = stderrWrite
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting ffmpeg: %w", err)
	}

	p = &Process{
		Stdout:  stdout,
		cmd:     cmd,
		input:   input,
		extra:   extra,
		tail:    newRingTail(),
		markers: &markerWatch{},
		scanned: scanned,
		stopped: stopped,
	}
	p.status.Store(noExitStatus)
	go func() {
		defer close(p.scanned)
		defer func() { _ = stderrRead.Close() }()
		drainStderr(ctx, stderrRead, p.tail, p.markers)
	}()
	p.followProgress(opts.Progress)
	p.tee(opts.PCM)

	return p, nil
}

func (p *Process) tee(w io.Writer) {
	p.teed = make(chan struct{})
	feed := p.extraAt(pcmFD)
	if feed == nil {
		close(p.teed)
		return
	}
	go func() {
		defer close(p.teed)
		defer func() { _ = feed.Close() }()
		if _, err := io.Copy(w, feed); err != nil {
			// A consumer that quit must not stall ffmpeg on a full pipe.
			_, _ = io.Copy(io.Discard, feed)
		}
	}()
}

func (p *Process) followProgress(step func(media.Progress)) {
	p.drained = make(chan struct{})
	feed := p.extraAt(progressFD)
	if feed == nil {
		close(p.drained)
		return
	}
	go func() {
		defer close(p.drained)
		defer func() { _ = feed.Close() }()
		watchProgress(feed, func(sample media.Progress) {
			p.sample.Store(&sample)
			if step != nil {
				step(sample)
			}
		})
	}()
}

func (p *Process) extraAt(fd int) io.ReadCloser {
	i := fd - firstExtraFD
	if i < 0 || i >= len(p.extra) {
		return nil
	}
	return p.extra[i]
}

// Progress returns the latest sample. Zero value = not yet muxed; whole pair is comparable.
func (p *Process) Progress() media.Progress {
	if s := p.sample.Load(); s != nil {
		return *s
	}
	return media.Progress{}
}

func (p *Process) Wait() error {
	err := p.cmd.Wait()
	// Publish exit status for Evidence reads from other goroutines.
	status, signaled := p.exitStatus()
	p.status.Store(int64(status))
	p.signaled.Store(signaled)
	// Release any platform transport still waiting for the exited child.
	finishSideOutputs(p.extra)
	// Then join progress (last sample still in pipe); step writes to work dir caller will remove.
	<-p.drained
	// And join stderr drain (ffmpeg's error path is still in the pipe).
	<-p.scanned
	<-p.teed
	if p.input != nil {
		if failure := p.input.failure.Load(); failure != nil {
			err = errors.Join(err, *failure)
		}
	}
	return err
}

// Kill signals the process to stop. It is idempotent and safe after exit.
func (p *Process) Kill() {
	_ = p.cmd.Cancel()
}

// kill marks castor's own kill only while stderr is open: an exited, unreaped process still accepts one.
func kill(proc *os.Process, exited <-chan struct{}, stopped *atomic.Bool) error {
	select {
	case <-exited:
		return os.ErrProcessDone
	default:
	}
	// Set first, so a Wait reaping what this kill ended already reads it as castor's.
	stopped.Store(true)
	err := proc.Kill()
	if err != nil {
		stopped.Store(false)
	}
	return err
}

// exitStatus reads the reaped status and whether its own signal ended it; castor's kill leaves no status.
func (p *Process) exitStatus() (status int, signaled bool) {
	state := p.cmd.ProcessState
	ws, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		// Windows has no signals: its kill is exit code 1, told from a failure only by castor having sent it.
		if runtime.GOOS == "windows" && p.stopped.Load() {
			return noExitStatus, false
		}
		return state.ExitCode(), false
	}
	// Teardown kills before it waits, so a crash already reaped still names its own signal.
	if ws.Signal() == syscall.SIGKILL && p.stopped.Load() {
		return noExitStatus, false
	}
	return signalExitBase + int(ws.Signal()), true
}

// observedInput preserves source errors that os/exec otherwise replaces with a
// nonzero process exit. EOF is ordinary completion, never a failure.
type observedInput struct {
	io.Reader
	failure atomic.Pointer[error]
}

func (r *observedInput) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		saved := err
		r.failure.Store(&saved)
	}
	return n, err
}
