package cast

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/cast/spool"
	"github.com/stupside/castor/internal/cast/whisper"
)

// pull is the running upstream download. Exactly one pull touches the source
// URL per cast: it remuxes the stream into the spool (codec copy — cheap),
// paced like a buffering player, and, when requested, tees a PCM audio feed
// for the transcriber. Everything downstream reads local data, so the CDN
// sees a single well-behaved client and can never interrupt playback of
// what's already spooled.
type pull struct {
	// pcm is the mono s16le audio feed, nil unless requested. The consumer
	// must keep draining it until EOF — backpressure on this pipe throttles
	// the whole download.
	pcm io.ReadCloser

	proc  *ffmpeg.Process
	spool *spool.Spool
	done  chan struct{}
	err   error
}

// startPull launches the upstream ffmpeg. Its mpegts output lands in sp; the
// optional PCM output is exposed as pull.pcm.
func startPull(ctx context.Context, cfg TranscodeConfig, plan Plan, sp *spool.Spool, wantPCM bool) (*pull, error) {
	args := ffmpeg.PullArgs(ffmpeg.PullOptions{
		SourceURL:       plan.SourceURL,
		SourceHeaders:   plan.SourceHeaders,
		RWTimeoutMicros: cfg.RWTimeout.Microseconds(),
		Verbose:         slog.Default().Enabled(ctx, slog.LevelDebug),
		PCM:             wantPCM,
		PCMSampleRate:   whisper.SampleRate,
	})

	var opts []ffmpeg.StartOption
	if wantPCM {
		opts = append(opts, ffmpeg.WithExtraPipe())
	}
	proc, err := ffmpeg.Start(ctx, cfg.FFmpegPath, args, opts...)
	if err != nil {
		return nil, fmt.Errorf("starting puller ffmpeg: %w", err)
	}

	slog.InfoContext(ctx, "upstream pull started",
		"source", plan.SourceURL.String(),
		"pcm", wantPCM,
	)
	// Full invocation at debug so the pull can be reproduced by hand
	// (ffmpeg <args>) to isolate source/network from the rest of the pipeline.
	slog.DebugContext(ctx, "puller ffmpeg command", "path", cfg.FFmpegPath, "args", args)

	p := &pull{pcm: proc.Extra, proc: proc, spool: sp, done: make(chan struct{})}
	go p.logProgress(ctx)
	go p.run(ctx)
	return p, nil
}

// run copies the remuxed stream into the spool and settles the pull's
// terminal state. The spool's write side is always closed on return.
func (p *pull) run(ctx context.Context) {
	defer close(p.done)
	_, copyErr := io.Copy(p.spool, p.proc.Stdout)
	waitErr := p.proc.Wait()

	err := cmp.Or(copyErr, waitErr)
	if err != nil && ctx.Err() == nil {
		p.proc.LogStderrTail(ctx, "puller ffmpeg stderr")
		err = fmt.Errorf("upstream pull: %w", err)
	} else if ctx.Err() != nil {
		err = ctx.Err()
	}
	p.err = err
	p.spool.CloseWrite(err)

	if err == nil {
		slog.InfoContext(ctx, "upstream pull complete", "spooled_bytes", p.spool.Size())
	}
}

// logProgress reports the download at INFO every few seconds — a rate of 0
// makes a throttled or stalled CDN immediately visible instead of a silent
// hang.
func (p *pull) logProgress(ctx context.Context) {
	const interval = 10 * time.Second
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var last int64
	for {
		select {
		case <-p.done:
			return
		case <-ctx.Done():
			return
		case <-tick.C:
			size := p.spool.Size()
			slog.DebugContext(ctx, "pull progress",
				"spooled_bytes", size,
				"rate_bytes_per_sec", (size-last)/int64(interval.Seconds()),
			)
			last = size
		}
	}
}

// Done is closed when the download has finished (cleanly or not) and the
// spool's write side is closed.
func (p *pull) Done() <-chan struct{} { return p.done }

// Err returns the terminal download error, if any. Only valid after Done.
func (p *pull) Err() error { return p.err }

// StderrTail returns the most recent lines of the puller ffmpeg's stderr,
// retained even while the download is still running. The playback gate uses
// this to explain a pre-playback stall: when the gate times out, ffmpeg is
// killed by context cancellation, so its own error path (which would
// otherwise dump these lines) never runs. An empty tail means ffmpeg
// connected but emitted nothing; lines like "Server returned 403/404" mean
// the link is expired or blocked.
func (p *pull) StderrTail() []string { return p.proc.StderrTail() }
