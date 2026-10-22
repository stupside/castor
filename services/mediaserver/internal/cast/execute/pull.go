package execute

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/cast/teardown"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

type pull struct {
	// pcm is the transcription's feed and pcmOut its write end, both nil where the read tees none.
	pcm    io.ReadCloser
	pcmOut *io.PipeWriter

	floor codec.Plan
	spool *storage.Spool
	proc  *ffmpeg.Process

	// done is closed once the read has ended, publishing err with it.
	done chan struct{}
	err  error
}

var _ health.Producer = (*pull)(nil)

// pullSpec is everything one upstream read starts with.
type pullSpec struct {
	live       bool
	ffmpegPath string
	source     encoding.ProgramSource
	probe      media.ProbeInfo
	spoolPath  string
	floor      codec.Plan
	// pcmRate is the rate of the PCM tee a transcription reads, zero for none.
	pcmRate int
}

func startPull(ctx context.Context, spec pullSpec) (*pull, error) {
	openSpool := storage.NewSpool
	if spec.live {
		openSpool = storage.NewLiveSpool
	}
	sp, err := openSpool(spec.spoolPath)
	if err != nil {
		return nil, err
	}
	var (
		pcm    io.ReadCloser
		pcmOut *io.PipeWriter
	)
	if spec.pcmRate > 0 {
		r, w := io.Pipe()
		pcm, pcmOut = r, w
	}
	proc, err := startPuller(ctx, spec, pcmOut)
	if err != nil {
		sp.CloseWrite(err)
		_ = sp.Close()
		if pcmOut != nil {
			_ = pcmOut.CloseWithError(err)
			_ = pcm.Close()
		}
		return nil, err
	}
	p := &pull{pcm: pcm, pcmOut: pcmOut, floor: spec.floor, spool: sp, proc: proc, done: make(chan struct{})}

	go p.run(ctx)
	return p, nil
}

// startPuller runs the read's ffmpeg, teeing PCM into pcmOut where there is one.
func startPuller(ctx context.Context, spec pullSpec, pcmOut *io.PipeWriter) (*ffmpeg.Process, error) {
	cmd, err := encoding.PullArgs(encoding.PullOptions{
		Source:        spec.source,
		Probe:         spec.probe,
		Video:         spec.floor.Video,
		Audio:         spec.floor.Audio,
		Verbose:       slog.Default().Enabled(ctx, slog.LevelDebug),
		PCMSampleRate: spec.pcmRate,
	})
	if err != nil {
		return nil, fmt.Errorf("building the puller command line: %w", err)
	}

	var opts ffmpeg.Options
	// Set only when there is a tee: a nil *PipeWriter in the field would read as a consumer.
	if pcmOut != nil {
		opts.PCM = pcmOut
	}
	proc, err := ffmpeg.Start(ctx, spec.ffmpegPath, cmd, opts)
	if err != nil {
		return nil, fmt.Errorf("starting puller ffmpeg: %w", err)
	}

	// Full invocation at debug so the pull can be reproduced by hand.
	slog.DebugContext(ctx, "puller ffmpeg command", "path", spec.ffmpegPath, "args", cmd.Args)
	return proc, nil
}

func (p *pull) run(ctx context.Context) {
	defer close(p.done)
	// The read owns this pipe. Cancellation must unblock the PCM tee even if
	// transcription has stopped consuming but has not returned yet.
	if p.pcmOut != nil {
		closePCM := sync.OnceFunc(func() { _ = p.pcmOut.CloseWithError(ctx.Err()) })
		stop := context.AfterFunc(ctx, closePCM)
		defer func() {
			if !stop() {
				closePCM()
			}
			_ = p.pcmOut.CloseWithError(p.err)
		}()
	}

	_, err := io.Copy(p.spool, p.proc.Stdout)
	if err != nil {
		// A reader nobody drains blocks on its pipe, so Wait would never return.
		p.proc.Kill()
	}
	// A clean exit is not a complete read: the demuxer skips or truncates media and still exits 0.
	processErr := p.proc.Wait()
	if p.proc.Evidence().ExitStatus < 0 {
		processErr = nil
	}
	err = teardown.Failures(ctx, errors.Join(err, processErr, p.proc.SilentFailure()))
	if err != nil {
		err = fmt.Errorf("upstream pull: %w", err)
	}
	p.err = err
	// A stopped read is truncated: a killed ffmpeg ends its copy as cleanly as a finished one.
	p.spool.CloseWrite(cmp.Or(err, ctx.Err()))

	if err == nil && ctx.Err() == nil {
		slog.InfoContext(ctx, "upstream pull complete", "spooled_bytes", p.spool.Size())
	}
}

// Progress is the sample the process keeps of its own -progress feed (see ffmpeg.Process.Progress).
func (p *pull) Progress() media.Progress { return p.proc.Progress() }

// Done is closed once the download has finished, cleanly or not, and the spool's write side with it.
func (p *pull) Done() <-chan struct{} { return p.done }

func (p *pull) Err() error {
	select {
	case <-p.done:
		return p.err
	default:
		return nil
	}
}

func (p *pull) Evidence() []string { return p.proc.Evidence().Lines }

// lostMedia reports a read that ended short of what its source declared.
func (p *pull) lostMedia() bool { return p.proc.LostMedia() }

func (p *pull) exitStatus() int { return p.proc.Evidence().ExitStatus }

// copying is the halves this read passed through untouched, which is what a recovery must stop asking for.
func (p *pull) copying() media.Axes { return p.floor.Copied() }
