package core

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/stupside/castor/internal/cast/deliver/hlsserve"
	"github.com/stupside/castor/internal/cast/deliver/replay"
	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/media"
)

// FinishEncoder tears down the encode process: close its output, wait for
// exit, and surface stderr forensics on a failure we didn't cause ourselves
// (a cancelled context means we killed ffmpeg, e.g. Ctrl+C — not worth
// dumping the tail for).
func FinishEncoder(ctx context.Context, proc *ffmpeg.Process) {
	_ = proc.Stdout.Close()
	if err := proc.Wait(); err != nil && ctx.Err() == nil {
		proc.LogStderrTail(ctx, "ffmpeg stderr")
		slog.WarnContext(ctx, "ffmpeg exited with error", "error", err)
	}
}

// ServeToDevice fronts stream with the replay-from-zero HTTP server, points
// the renderer at it, and blocks until the stream has been fully produced
// and delivered or ctx ends. outputContentType is what the device is told it
// is fetching (e.g. "video/mp2t" or "video/mp4").
func ServeToDevice(ctx context.Context, dev device.Device, localIP, outputContentType string, stream io.Reader, workDir string) error {
	fmtInfo, ok := media.FormatForContentType(outputContentType)
	if !ok {
		return fmt.Errorf("no format info for output content type %q", outputContentType)
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

// ServeHLSToDevice fronts a live HLS directory the encoder is packaging into
// workDir with the HLS server, points the renderer at the playlist, and blocks
// until the stream is fully delivered or ctx ends. Unlike ServeToDevice, the
// encoder writes files rather than a pipe, so this owns proc's lifecycle: it
// drains the unused stdout, and a goroutine waits for the encoder to exit and
// then tells the server the producer is done (calling FinishEncoder as well
// would double-Wait). The renderer is pointed at the playlist only once it
// exists on disk, so it is never handed a 404.
func ServeHLSToDevice(ctx context.Context, dev device.Device, localIP, workDir string, proc *ffmpeg.Process) error {
	// HLS output lands in files, not pipe:1; drain the unused stdout so ffmpeg
	// never blocks on a full pipe.
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
// is not pointed at a 404.
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
