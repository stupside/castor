package cast

import (
	"context"
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

	proc, err := ffmpeg.Start(ctx, cfg.Transcode.FFmpegPath, ffmpeg.EncodeArgs(opts))
	if err != nil {
		return fmt.Errorf("starting transcode: %w", err)
	}
	defer finishEncoder(ctx, proc)

	return serveToDevice(ctx, plan, dev, localIP, proc.Stdout, workDir)
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

// serveToDevice fronts stream with the replay-from-zero HTTP server, points
// the renderer at it, and blocks until the stream has been fully produced
// and delivered or ctx ends.
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
	return srv.Wait(ctx)
}
