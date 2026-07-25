package core

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/stupside/castor/internal/cast/deliver/hlsserve"
	"github.com/stupside/castor/internal/cast/deliver/replay"
	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/media"
)

// This file is the served-cast delivery driver. It is device-blind and carries no
// per-delivery code path in its control flow: Serve looks the delivery mechanism
// up from the format's DeliveryKind (data) and drives it uniformly. Each
// mechanism is one opener behind the deliveries table, so adding a delivery (or a
// caption sidecar, which is a second Sink at Serve's single Play step) is new data
// plus a small impl, not another Serve* function and not a content-type branch.

// Sink is a running local server fronting a produced stream for one cast: it
// exposes the URL the renderer fetches and blocks until the stream is fully
// delivered. Closing it is the opener's job (its teardown holds the concrete
// server), so the driver only ever needs these two. Both replay.Server and
// hlsserve.Server satisfy it unchanged.
type Sink interface {
	URL() *url.URL
	Wait(ctx context.Context) error
}

// OpenParams are the device-blind inputs to open a delivery: the encoder to run
// and how its input/output is wired, where to serve from, and the format that
// selects the mechanism. Headers are filled by Serve from the device, not here.
type OpenParams struct {
	FFmpegPath string
	Opts       ffmpeg.EncodeOptions
	StartOpts  []ffmpeg.StartOption
	LocalIP    string
	WorkDir    string
	Format     media.FormatInfo
	// OnStarted, if set, runs immediately after the encoder starts (before the
	// server is fronted), so a caller can wire a concurrent consumer of a second
	// output pipe (the spool path follows -progress on proc.Extra) without that
	// coupling leaking into this package.
	OnStarted func(*ffmpeg.Process)
}

// session is one opened delivery: the running server, an optional readiness gate
// (nil when the URL is usable immediately), and the teardown that stops the
// encoder and server. It lets Serve stay branch-free over the two mechanisms.
type session struct {
	sink     Sink
	ready    func(ctx context.Context) error
	teardown func()
}

// opener starts an encoder and fronts it, returning the opened session. On
// failure it fully cleans up the encoder itself and returns the error, so Serve
// never has to tear down a half-open delivery.
type opener func(ctx context.Context, p OpenParams, headers map[string]string) (*session, error)

// deliveries maps a format's DeliveryKind to the opener that serves it. This is
// the whole per-delivery dispatch: no switch, no content-type conditional.
var deliveries = map[media.DeliveryKind]opener{
	media.DeliverStream:    openStream,
	media.DeliverSegmented: openSegmented,
}

// Serve runs one served cast end to end and is the single delivery entry point:
// pick the mechanism from the format's DeliveryKind, open it, wait until the
// device can be handed a URL, play, and block until delivered or ctx ends,
// tearing the encoder and server down on every return. It names no device family.
func Serve(ctx context.Context, dev device.Device, p OpenParams) error {
	open, ok := deliveries[p.Format.Delivery]
	if !ok {
		return fmt.Errorf("no delivery mechanism for format %q", p.Format.ContentType)
	}

	sess, err := open(ctx, p, dev.StreamHeaders(p.Format.ContentType))
	if err != nil {
		return err
	}
	defer sess.teardown()

	if sess.ready != nil {
		if err := sess.ready(ctx); err != nil {
			return err
		}
	}

	streamURL := sess.sink.URL()
	slog.InfoContext(ctx, "starting playback", "url", streamURL.String(), "content_type", p.Format.ContentType)
	if err := dev.Play(ctx, streamURL, p.Format.ContentType); err != nil {
		return fmt.Errorf("starting playback: %w", err)
	}
	slog.InfoContext(ctx, "streaming to device, press Ctrl+C to stop")
	return sess.sink.Wait(ctx)
}

// openStream serves a single growing output over the replay-from-zero server: the
// encoder writes pipe:1, which the server spools and replays to every client from
// byte 0. The URL is usable immediately (no readiness gate). Teardown closes the
// server then the encoder, so nothing is left writing when the caller removes the
// work directory.
func openStream(ctx context.Context, p OpenParams, headers map[string]string) (*session, error) {
	proc, err := ffmpeg.Start(ctx, p.FFmpegPath, ffmpeg.EncodeArgs(p.Opts), p.StartOpts...)
	if err != nil {
		return nil, fmt.Errorf("starting transcode: %w", err)
	}
	if p.OnStarted != nil {
		p.OnStarted(proc)
	}

	srv, err := replay.New(replay.Config{
		LocalIP:     p.LocalIP,
		ContentType: p.Format.ContentType,
		Extension:   p.Format.Extension,
		Headers:     headers,
		SpoolPath:   filepath.Join(p.WorkDir, "out"+p.Format.Extension),
	}, proc.Stdout)
	if err != nil {
		finishEncoder(ctx, proc)
		return nil, fmt.Errorf("starting stream server: %w", err)
	}

	return &session{
		sink:     srv,
		teardown: func() { _ = srv.Close(); finishEncoder(ctx, proc) },
	}, nil
}

// openSegmented serves a live HLS directory: the encoder writes the playlist and
// rolling segments into the work directory, which the HLS server fronts. Because
// the output is files (not pipe:1), this fully owns the encoder lifecycle: a
// single goroutine drains the unused stdout to EOF and only then Waits (honoring
// os/exec's no-Wait-before-reads contract), then signals the server and the
// readiness gate. The device is handed the playlist only once it exists (the gate
// fails fast if the encoder dies first). Teardown kills the encoder, joins the
// goroutine, then closes the server, so nothing writes into the work directory
// after the caller removes it.
func openSegmented(ctx context.Context, p OpenParams, _ map[string]string) (*session, error) {
	startOpts := append(slices.Clone(p.StartOpts), ffmpeg.WithWorkDir(p.WorkDir))
	proc, err := ffmpeg.Start(ctx, p.FFmpegPath, ffmpeg.EncodeArgs(p.Opts), startOpts...)
	if err != nil {
		return nil, fmt.Errorf("starting transcode: %w", err)
	}
	if p.OnStarted != nil {
		p.OnStarted(proc)
	}

	srv, err := hlsserve.New(hlsserve.Config{
		LocalIP:  p.LocalIP,
		Dir:      p.WorkDir,
		Playlist: media.HLSPlaylistName,
	})
	if err != nil {
		proc.Kill()
		_ = proc.Wait()
		return nil, fmt.Errorf("starting HLS server: %w", err)
	}

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

	return &session{
		sink:     srv,
		ready:    func(ctx context.Context) error { return waitForPlaylist(ctx, p.WorkDir, exited) },
		teardown: func() { proc.Kill(); wg.Wait(); _ = srv.Close() },
	}, nil
}

// finishEncoder tears down a pipe-fed encoder: close its output (the encoder gets
// EPIPE and exits), wait for exit, and surface stderr forensics on a failure we
// didn't cause ourselves (a cancelled context means we killed ffmpeg, e.g.
// Ctrl+C, not worth dumping the tail for).
func finishEncoder(ctx context.Context, proc *ffmpeg.Process) {
	_ = proc.Stdout.Close()
	if err := proc.Wait(); err != nil && ctx.Err() == nil {
		proc.LogStderrTail(ctx, "ffmpeg stderr")
		slog.WarnContext(ctx, "ffmpeg exited with error", "error", err)
	}
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
