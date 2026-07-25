package core

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
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
// encoder writes files rather than a pipe, so this fully owns proc's lifecycle:
// on every return it kills the encoder and joins its reaper goroutine, so no
// ffmpeg is left running (or writing into workDir, which the caller removes) and
// no goroutine leaks, even when dev.Play or the HLS server fails. The renderer is
// pointed at the playlist only once it exists on disk, so it is never handed a
// 404; if the encoder dies before writing one, the gate fails fast instead of
// hanging.
func ServeHLSToDevice(ctx context.Context, dev device.Device, localIP, workDir string, proc *ffmpeg.Process) error {
	srv, err := hlsserve.New(hlsserve.Config{
		LocalIP:  localIP,
		Dir:      workDir,
		Playlist: media.HLSPlaylistName,
	})
	if err != nil {
		proc.Kill()
		_ = proc.Wait()
		return fmt.Errorf("starting HLS server: %w", err)
	}
	defer srv.Close()

	// One goroutine owns the encoder end to end: drain the unused stdout to EOF
	// (HLS output is files, not pipe:1) and only then Wait, satisfying os/exec's
	// "no Wait before reads complete" contract without a second racing goroutine.
	// It closes exited so the playlist gate and the teardown both observe the exit.
	var wg sync.WaitGroup
	exited := make(chan struct{})
	wg.Go(func() {
		_, _ = io.Copy(io.Discard, proc.Stdout)
		if err := proc.Wait(); err != nil && ctx.Err() == nil {
			proc.LogStderrTail(ctx, "ffmpeg stderr")
			slog.WarnContext(ctx, "ffmpeg exited with error", "error", err)
		}
		srv.ProducerDone()
		close(exited)
	})
	// Deterministic teardown on every path: stop the encoder (idempotent if it
	// already exited) and wait for the goroutine, so the caller can remove workDir
	// with nothing still writing to it.
	defer func() {
		proc.Kill()
		wg.Wait()
	}()

	if err := waitForPlaylist(ctx, workDir, exited); err != nil {
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

// waitForPlaylist blocks until the muxer has written the playlist (so the device
// is not pointed at a 404), the encoder exits first (fail fast rather than poll
// forever for a playlist that will never appear), or ctx ends.
func waitForPlaylist(ctx context.Context, workDir string, producerExited <-chan struct{}) error {
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
		case <-producerExited:
			// The encoder exited before a playlist appeared. Re-check once in case
			// it finalized one as it went, else fail with a clear error (the exit
			// reason was already logged) instead of polling for a file that will
			// never be written.
			if _, err := os.Stat(playlist); err == nil {
				return nil
			}
			return fmt.Errorf("encoder exited before producing the HLS playlist")
		case <-tick.C:
		}
	}
}
