package execute

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/cast/splice"
	"github.com/stupside/castor/services/mediaserver/internal/cast/splice/mpegts"
	"github.com/stupside/castor/services/mediaserver/internal/cast/teardown"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

type pull struct {
	// pcm is the transcription's feed and pcmOut its write end, both nil where the read tees none.
	pcm    io.ReadCloser
	pcmOut *io.PipeWriter

	floor codec.Plan
	// probe is what the source was measured to hold, reused by a read of it from elsewhere.
	probe media.ProbeInfo
	spool *storage.Spool
	// timeline is a read keeping the source's clock, so its media is counted from its keyframes: ffmpeg's account starts at the first picture's time.
	timeline bool
	// index lists the spool's keyframes, which a delivery cuts at; nil for a live read, which keeps no past.
	index *splice.Index
	proc  *ffmpeg.Process

	stop func()
	// begins is where in the program the read started.
	begins time.Duration
	// continuedBy is the read that took this one's place, which is then what is said of the read.
	continuedBy atomic.Pointer[continuation]

	// done is closed once this read has ended, publishing err with it; Done follows the reads that carried it on.
	done chan struct{}
	err  error
}

var _ health.Producer = (*pull)(nil)

// continuation is a read taking another's place: where it got to, for another rung of the source, or where the device asked, for a seek.
type continuation struct {
	next *pull
	seek bool
}

// spoolFraming is how the read's buffer, always MPEG-TS, is cut.
func spoolFraming() splice.Framing { return mpegts.Framing{} }

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
	// keepTimeline keeps the source's clock, so the read can be carried on from another rung of the same program.
	keepTimeline bool
	begins       time.Duration
}

func startPull(parent context.Context, spec pullSpec) (*pull, error) {
	ctx, stop := context.WithCancel(parent)
	openSpool := storage.NewSpool
	if spec.live {
		openSpool = storage.NewLiveSpool
	}
	sp, err := openSpool(spec.spoolPath)
	if err != nil {
		stop()
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
		stop()
		sp.CloseWrite(err)
		_ = sp.Close()
		if pcmOut != nil {
			_ = pcmOut.CloseWithError(err)
			_ = pcm.Close()
		}
		return nil, err
	}
	p := &pull{pcm: pcm, pcmOut: pcmOut, floor: spec.floor, probe: spec.probe, spool: sp, proc: proc, stop: stop, begins: spec.begins, timeline: spec.keepTimeline && !spec.live, done: make(chan struct{})}
	if !spec.live {
		p.index = splice.NewIndex(spoolFraming())
	}

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
		KeepTimeline:  spec.keepTimeline,
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

	var spooled io.Writer = p.spool
	if p.index != nil {
		// The spool first: a cut is only listed once its bytes can be read.
		spooled = io.MultiWriter(p.spool, p.index)
		defer p.index.Close()
	}
	_, err := io.Copy(spooled, p.proc.Stdout)
	if err != nil {
		// A reader nobody drains blocks on its pipe, so Wait would never return.
		p.proc.Kill()
	}
	// A clean exit is not a complete read: the demuxer skips or truncates media and still exits 0.
	processErr := p.proc.Wait()
	if p.proc.Evidence().ExitStatus < 0 {
		processErr = nil
	}
	if p.continuedBy.Load() != nil {
		// Stopped on purpose, its place taken: what carried it on speaks for the read from now on.
		p.spool.CloseWrite(nil)
		return
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

// Progress is the process's -progress sample, counted from where this read began, whichever read carried it on.
func (p *pull) Progress() media.Progress {
	made, _ := p.progress()
	return made
}

// progress is Progress, and whether a seek lies among the reads that carried this one on.
func (p *pull) progress() (media.Progress, bool) {
	made := p.proc.Progress()
	if p.timeline && p.index != nil {
		made.Position = p.index.Extent()
	}
	c := p.continuedBy.Load()
	if c == nil {
		return made, false
	}
	later, sought := c.next.progress()
	later.Position += c.next.begins - p.begins
	sought = sought || c.seek
	if !sought && later.Position < made.Position {
		// Another rung starts a little behind where this one got to, and what is said of the read does not go back for it.
		later.Position = made.Position
	}
	return later, sought
}

// continueWith ends this read for next, another rung carrying on where it got to.
func (p *pull) continueWith(next *pull) { p.carryOn(&continuation{next: next}) }

// seekWith ends this read for next, which reads from where the device asked.
func (p *pull) seekWith(next *pull) { p.carryOn(&continuation{next: next, seek: true}) }

func (p *pull) carryOn(c *continuation) {
	p.continuedBy.Store(c)
	p.stop()
}

// tail is the read that carries on from this one, or this one.
func (p *pull) tail() *pull {
	for c := p.continuedBy.Load(); c != nil; c = p.continuedBy.Load() {
		p = c.next
	}
	return p
}

// Done is closed once the read that speaks for this one has finished, cleanly or not, and its spool's write side with it.
func (p *pull) Done() <-chan struct{} { return p.tail().done }

func (p *pull) Err() error {
	t := p.tail()
	select {
	case <-t.done:
		return t.err
	default:
		return nil
	}
}

// joined waits for this read alone to end, whoever carried it on, and is how its owner releases it.
func (p *pull) joined() error {
	<-p.done
	return p.err
}

func (p *pull) Evidence() []string { return p.tail().proc.Evidence().Lines }

// lostMedia reports a read that ended short of what its source declared.
func (p *pull) lostMedia() bool { return p.tail().proc.LostMedia() }

func (p *pull) exitStatus() int { return p.tail().proc.Evidence().ExitStatus }

// copying is the halves this read passed through untouched, which is what a recovery must stop asking for.
func (p *pull) copying() media.Axes { return p.floor.Copied() }
