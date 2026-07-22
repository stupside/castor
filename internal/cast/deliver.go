package cast

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/cast/replay"
	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/media"
)

// runDirect is the single-ffmpeg path for devices that just need a container
// change (Chromecast remux): network input, no whisper, no input spool —
// though the output still spools so the device can replay from 0.
func runDirect(ctx context.Context, cfg Config, plan Plan, dev device.Device, localIP string) error {
	workDir, err := os.MkdirTemp("", "castor-")
	if err != nil {
		return fmt.Errorf("creating work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	opts := *plan.Transcode
	opts.SourceURL = plan.SourceURL
	opts.SourceHeaders = plan.SourceHeaders
	opts.RWTimeoutMicros = cfg.Transcode.RWTimeout.Microseconds()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	proc, err := ffmpeg.Start(runCtx, cfg.Transcode.FFmpegPath, ffmpeg.EncodeArgs(opts))
	if err != nil {
		return fmt.Errorf("starting transcode: %w", err)
	}
	defer finishEncoder(runCtx, proc)

	err = serveToDevice(runCtx, plan, dev, localIP, proc.Stdout, workDir)
	if errors.Is(err, errPlaybackDone) {
		// Deliberate teardown, not a failure: cancel so finishEncoder treats
		// the encoder's death like a Ctrl+C instead of dumping forensics.
		cancel()
		return nil
	}
	return err
}

// finishEncoder tears down the encode process: close its output, wait for
// exit, and surface stderr forensics on a failure we didn't cause ourselves
// (a cancelled context means we killed ffmpeg, e.g. Ctrl+C — not worth
// dumping the tail for).
func finishEncoder(ctx context.Context, proc *ffmpeg.Process) {
	_ = proc.Stdout.Close()
	if err := proc.Wait(); err != nil && ctx.Err() == nil {
		proc.LogStderrTail(ctx, "ffmpeg stderr")
		slog.WarnContext(ctx, "ffmpeg exited with error", "error", err)
	}
}

// errPlaybackDone ends delivery when the renderer itself reports playback
// over — the user stopped it on the device, or it finished playing there.
// Callers treat it as a clean exit and cancel their pipeline context first,
// so encoder teardown reads as deliberate rather than as a failure.
var errPlaybackDone = errors.New("device reported playback done")

// serveToDevice fronts stream with the replay-from-zero HTTP server, points
// the renderer at it, and blocks until the stream has been fully produced
// and delivered, the renderer reports playback over (errPlaybackDone), or
// ctx ends.
func serveToDevice(ctx context.Context, plan Plan, dev device.Device, localIP string, stream io.Reader, workDir string) error {
	fmtInfo, ok := media.FormatForContentType(plan.OutputContentType)
	if !ok {
		return fmt.Errorf("no format info for output content type %q", plan.OutputContentType)
	}

	srv, err := replay.New(replay.Config{
		LocalIP:     localIP,
		ContentType: fmtInfo.ContentType,
		Extension:   fmtInfo.Extension,
		Headers:     dev.StreamHeaders(fmtInfo.ContentType),
		SpoolPath:   filepath.Join(workDir, "out"+fmtInfo.Extension),
	}, stream)
	if err != nil {
		return fmt.Errorf("starting stream server: %w", err)
	}
	defer srv.Close()

	streamURL := srv.URL()
	slog.InfoContext(ctx, "starting playback", "url", streamURL.String(), "content_type", fmtInfo.ContentType)
	if err := dev.Play(ctx, streamURL, fmtInfo.ContentType); err != nil {
		return fmt.Errorf("starting playback: %w", err)
	}
	slog.InfoContext(ctx, "streaming to device, press Ctrl+C to stop")

	// The renderer can end the cast too: stopped on the device, or played to
	// the end there. WaitDone cancels the delivery wait with errPlaybackDone,
	// which is a clean exit rather than an error.
	waitCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go func() {
		if dev.WaitDone(waitCtx) == nil {
			cancel(errPlaybackDone)
		}
	}()

	err = srv.Wait(waitCtx)
	if err != nil && errors.Is(context.Cause(waitCtx), errPlaybackDone) {
		slog.InfoContext(ctx, "device reported playback done, stopping")
		return errPlaybackDone
	}
	return err
}
