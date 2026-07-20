package cast

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/cast/hlsserve"
	"github.com/stupside/castor/internal/cast/replay"
	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/media"
)

// runDirect is the single-ffmpeg path for devices that just need a container
// change (Chromecast, Roku). The output format's registry entry decides delivery:
// a spooled stream replayed from 0, or a segmented HLS directory.
func runDirect(ctx context.Context, cfg Config, plan Plan, dev device.Device, localIP string) error {
	workDir, err := os.MkdirTemp("", "castor-")
	if err != nil {
		return fmt.Errorf("creating work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	_, fmtInfo, ok := media.FormatForContentType(plan.OutputContentType)
	if !ok {
		return fmt.Errorf("no format info for output content type %q", plan.OutputContentType)
	}

	opts := *plan.Transcode
	opts.SourceURL = plan.SourceURL
	opts.SourceHeaders = plan.SourceHeaders
	opts.RWTimeoutMicros = cfg.Transcode.RWTimeout.Microseconds()

	if fmtInfo.Segmented {
		return runDirectHLS(ctx, cfg, plan, dev, localIP, workDir, opts)
	}

	proc, err := ffmpeg.Start(ctx, cfg.Transcode.FFmpegPath, ffmpeg.EncodeArgs(opts))
	if err != nil {
		return fmt.Errorf("starting transcode: %w", err)
	}
	defer finishEncoder(ctx, proc)

	return serveToDevice(ctx, plan, dev, localIP, proc.Stdout, workDir)
}

// runDirectHLS runs the encoder as an HLS packager into workDir and fronts it
// with the HLS server.
func runDirectHLS(ctx context.Context, cfg Config, plan Plan, dev device.Device, localIP, workDir string, opts ffmpeg.EncodeOptions) error {
	proc, err := ffmpeg.Start(ctx, cfg.Transcode.FFmpegPath, ffmpeg.EncodeArgs(opts), ffmpeg.WithWorkDir(workDir))
	if err != nil {
		return fmt.Errorf("starting transcode: %w", err)
	}
	// HLS writes files, not pipe:1; drain the unused stdout.
	go func() { _, _ = io.Copy(io.Discard, proc.Stdout) }()

	srv, err := hlsserve.New(hlsserve.Config{
		LocalIP:  localIP,
		Dir:      workDir,
		Playlist: media.HLSPlaylistName,
	})
	if err != nil {
		_ = proc.Stdout.Close()
		return fmt.Errorf("starting HLS server: %w", err)
	}
	defer srv.Close()

	// finishEncoder would double-Wait; own the process exit here instead.
	go func() {
		if err := proc.Wait(); err != nil && ctx.Err() == nil {
			proc.LogStderrTail(ctx, "ffmpeg stderr")
			slog.WarnContext(ctx, "ffmpeg exited with error", "error", err)
		}
		srv.ProducerDone()
	}()

	if err := waitForPlaylist(ctx, workDir); err != nil {
		return err
	}

	streamURL := srv.URL()
	slog.InfoContext(ctx, "starting playback", "url", streamURL.String(), "content_type", media.HLS)
	if err := dev.Play(ctx, streamURL, media.HLS); err != nil {
		return fmt.Errorf("starting playback: %w", err)
	}
	slog.InfoContext(ctx, "streaming to device, press Ctrl+C to stop")
	return srv.Wait(ctx)
}

// waitForPlaylist blocks until the muxer has written the playlist, so the device
// isn't pointed at a 404.
func waitForPlaylist(ctx context.Context, workDir string) error {
	playlist := filepath.Join(workDir, media.HLSPlaylistName)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(playlist); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
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
	_, fmtInfo, ok := media.FormatForContentType(plan.OutputContentType)
	if !ok {
		return fmt.Errorf("no format info for output content type %q", plan.OutputContentType)
	}

	srv, err := replay.New(replay.Config{
		LocalIP:     localIP,
		ContentType: fmtInfo.ContentType,
		Extension:   fmtInfo.Extension,
		Headers:     dev.StreamHeaders(fmtInfo.ContentType),
		SpoolPath:   filepath.Join(workDir, "out"+fmtInfo.Extension),
		SendRate:    plan.SendRate,
		SendBurst:   plan.SendBurst,
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
