package execute

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/teardown"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// production owns an encoder and its output packager. Done publishes the final
// result only after packaging and process reaping have both finished.
type production struct {
	proc *ffmpeg.Process
	done chan struct{}
	stop func()
	err  error
	// hung is the watch's verdict on an encoder it stopped, nil while it makes.
	hung atomic.Pointer[encoderHung]
}

// startProduction runs proc's output through write; unread, where the encoder reads castor's buffer, is what it has left there, which lets a silent encoder be told hung.
func startProduction(parent context.Context, proc *ffmpeg.Process, input io.Closer, unread func() int64, write func(context.Context, io.Reader) error) *production {
	ctx, cancel := context.WithCancel(parent)
	p := &production{proc: proc, done: make(chan struct{})}
	var out io.Reader = proc.Stdout
	releaseIO := sync.OnceFunc(func() {
		if input != nil {
			_ = input.Close()
		}
		_ = proc.Stdout.Close()
	})
	interrupt := sync.OnceFunc(func() { proc.Kill(); releaseIO() })
	p.stop = func() { cancel(); interrupt() }
	stopped := context.AfterFunc(ctx, interrupt)
	if unread != nil {
		clock := &waitClock{}
		out = timedOutput{Reader: proc.Stdout, clock: clock}
		go watchHang(ctx, hangWindow, func() time.Duration { return silent(clock.waited(), unread()) }, func(waited time.Duration) {
			p.hung.Store(&encoderHung{waited: waited})
			interrupt()
		})
	}
	go func() {
		defer close(p.done)
		defer cancel()
		defer func() { stopped(); interrupt() }()
		packaging := write(ctx, out)
		if packaging != nil {
			proc.Kill()
		}
		releaseIO()
		if hung := p.hung.Load(); hung != nil {
			// A hung encoder may sit in a driver call that holds off even a kill, so it is reaped without waiting on it.
			go func() { _ = proc.Wait() }()
			p.err = hung
			return
		}
		process := encoderResult(ctx, proc)
		p.err = errors.Join(teardown.Failures(ctx, packaging), process)
	}()
	return p
}

func (p *production) Done() <-chan struct{} { return p.done }
func (p *production) Err() error {
	select {
	case <-p.done:
		return p.err
	default:
		return nil
	}
}
func (p *production) Progress() media.Progress { return p.proc.Progress() }
func (p *production) Evidence() []string       { return p.proc.Evidence().Lines }
func (p *production) Close() error             { p.stop(); <-p.done; return p.err }
