package execute

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/stupside/castor/services/mediaserver/internal/cast/splice"
	"github.com/stupside/castor/services/mediaserver/internal/cast/teardown"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// production owns an encoder and its output packager. Done publishes the final
// result only after packaging and process reaping have both finished.
type production struct {
	proc *ffmpeg.Process
	// in is nil when the encoder reads the source itself.
	in   *splice.Cursor
	out  *awaited
	done chan struct{}
	stop func()
	err  error
}

func startProduction(parent context.Context, proc *ffmpeg.Process, in *splice.Cursor, hardware bool, write func(context.Context, io.Reader) error) *production {
	ctx, cancel := context.WithCancel(parent)
	p := &production{proc: proc, in: in, out: &awaited{Reader: proc.Stdout}, done: make(chan struct{})}
	releaseIO := sync.OnceFunc(func() {
		if in != nil {
			_ = in.Close()
		}
		_ = proc.Stdout.Close()
	})
	interrupt := sync.OnceFunc(func() { proc.Kill(); releaseIO() })
	p.stop = func() { cancel(); interrupt() }
	stopped := context.AfterFunc(ctx, interrupt)
	go func() {
		defer close(p.done)
		defer cancel()
		defer func() { stopped(); interrupt() }()
		packaging := write(ctx, p.out)
		if packaging != nil {
			proc.Kill()
		}
		releaseIO()
		process := encoderResult(ctx, proc, hardware, in != nil)
		p.err = errors.Join(teardown.Failures(ctx, packaging), process)
	}()
	return p
}

func (p *production) Done() <-chan struct{} { return p.done }

// Fed is an encoder castor waits on with buffer left for it to read.
func (p *production) Fed() bool {
	select {
	case <-p.done:
		return false
	default:
		return p.in != nil && p.out.waiting() && p.in.Behind()
	}
}
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
