package cast

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sync/errgroup"

	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/cast/spool"
	"github.com/stupside/castor/internal/cast/whisper"
	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/media"
)

// runSpooled is the read-once cast: puller → spool (+ PCM → whisper) → tail
// → encoder (drawtext burn-in) → replay server → device. Each stage lives in
// its own file; this function only wires them together.
//
// Lifecycle: a cancellable context fed into errgroup. Defer order matters and
// is LIFO — cancel runs first (signals every goroutine and kills both ffmpeg
// processes), g.Wait blocks until they've unwound, then a connected-but-
// unclaimed device is closed (its goroutine has finished, so the future is
// settled), and only then is the work directory removed.
func runSpooled(parentCtx context.Context, cfg Config, plan Plan, localIP string, connect deviceConnector) error {
	workDir, err := os.MkdirTemp("", "castor-")
	if err != nil {
		return fmt.Errorf("creating work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }() // registered first → runs last

	dev := newDeviceFuture()
	defer dev.closeUnclaimed()

	runCtx, cancel := context.WithCancel(parentCtx)
	g, ctx := errgroup.WithContext(runCtx)
	defer func() { _ = g.Wait() }()
	defer cancel() // registered last → runs first

	// Discover + connect the renderer concurrently with everything below. It
	// isn't needed until the playback gate opens, and SSDP discovery + connect
	// can take seconds — seconds the short-lived signed source URL can't spare.
	dev.connect(ctx, g, connect)

	sp, err := spool.New(filepath.Join(workDir, "spool.ts"))
	if err != nil {
		return err
	}

	// The subtitle decision must land before the pull starts: it determines
	// whether the puller opens a PCM output at all.
	subs := newSubtitles(ctx, cfg, plan, workDir)

	pl, err := startPull(ctx, cfg.Transcode, plan, sp, subs != nil)
	if err != nil {
		return err
	}
	if subs != nil {
		subs.transcribe(ctx, g, pl.pcm)
	}

	var tr *whisper.Transcriber
	if subs != nil {
		tr = subs.tr
	}
	if err := waitForPlayable(ctx, tr, sp, pl); err != nil {
		return err
	}

	// The renderer is needed now: its negotiated capabilities drive the
	// copy-vs-encode decision below. Discovery and connect have been running
	// since the top and overlap the whole pull+gate window, so this await almost
	// never blocks; if connect failed, ctx carries the cause.
	d, err := dev.await(ctx)
	if err != nil {
		return err
	}
	defer d.Close()

	// The spool now holds real bytes. Probe it locally (no upstream round-trip)
	// to decide whether the source video can be stream-copied into MPEG-TS or
	// must be re-encoded. A failed or partial probe leaves srcInfo zero, which
	// CanCopyVideo rejects, i.e. it falls back to a transcode.
	srcInfo, err := ffmpeg.Probe(ctx, cfg.Resolver.FFprobePath, sp.Path())
	if err != nil {
		slog.WarnContext(ctx, "spool probe failed; will re-encode video", "error", err)
	}

	opts := *plan.Transcode
	hasSubs := subs != nil
	if !hasSubs && d.Capabilities().CanCopyVideo(srcInfo) {
		// Copy path: leave the video bitstream untouched (near-zero CPU); audio
		// is still re-encoded to AAC (the template sets that) so Samsung accepts
		// it. Pace from the source's own bit rate.
		opts.VideoEncoder = nil
		opts.VideoBitrate = ""
		opts.VideoMaxHeight = 0
		bitsPerSec := srcInfo.BitRate
		if bitsPerSec <= 0 {
			bitsPerSec = encodedBitrateBPS(dlnaVideoBitrate, dlnaAudioBitrate)
		}
		plan.SendRate, plan.SendBurst = dlnaPacing(bitsPerSec)
	} else {
		// Re-encode with the best encoder for this host: a verified hardware
		// encoder, else the software baseline (which always exists for H.264).
		// The codec is H.264 for now; picking the most efficient codec the
		// renderer advertises is the next step, once capabilities are discovered.
		enc, _ := ffmpeg.SelectEncoder(ctx, cfg.Transcode.FFmpegPath, media.CodecH264)
		opts.VideoEncoder = &enc
		plan.SendRate, plan.SendBurst = dlnaPacing(encodedBitrateBPS(dlnaVideoBitrate, dlnaAudioBitrate))
	}

	videoCodec := "copy"
	if opts.VideoEncoder != nil {
		videoCodec = opts.VideoEncoder.Name
	}
	slog.InfoContext(ctx, "dlna encode decision",
		"video_codec", videoCodec,
		"source_codec", string(srcInfo.VideoCodec),
		"source_profile", srcInfo.VideoProfile,
		"source_level", srcInfo.VideoLevel,
		"source_height", srcInfo.VideoHeight,
		"subtitles", hasSubs,
	)

	opts.PipeFormat = "mpegts"
	if subs != nil {
		if err := subs.attach(&opts); err != nil {
			return err
		}
	}

	tail, err := sp.Tail(ctx)
	if err != nil {
		return err
	}
	defer tail.Close()

	startOpts := []ffmpeg.StartOption{ffmpeg.WithStdin(tail)}
	if subs != nil {
		startOpts = append(startOpts, ffmpeg.WithExtraPipe()) // -progress on fd 3
	}
	proc, err := ffmpeg.Start(ctx, cfg.Transcode.FFmpegPath, ffmpeg.EncodeArgs(opts), startOpts...)
	if err != nil {
		return fmt.Errorf("starting transcode: %w", err)
	}
	defer finishEncoder(ctx, proc)

	if subs != nil && proc.Extra != nil {
		subs.follow(ctx, g, proc.Extra)
	}

	return serveToDevice(ctx, plan, d, localIP, proc.Stdout, workDir)
}

// deviceFuture is the async renderer connection. The connect goroutine runs
// in the pipeline's errgroup — a failure cancels the group with that error as
// the cause — and the result waits in a one-slot channel until awaited. A
// device connected but never claimed (e.g. the gate fails first) is closed by
// closeUnclaimed during teardown.
type deviceFuture struct {
	ch chan device.Device
}

func newDeviceFuture() *deviceFuture {
	return &deviceFuture{ch: make(chan device.Device, 1)}
}

func (f *deviceFuture) connect(ctx context.Context, g *errgroup.Group, connect deviceConnector) {
	g.Go(func() error {
		dev, err := connect(ctx)
		if err != nil {
			return err
		}
		select {
		case f.ch <- dev:
		case <-ctx.Done():
			_ = dev.Close()
		}
		return nil
	})
}

// await blocks until the device is connected or ctx ends. context.Cause
// surfaces the real reason when a concurrent stage cancelled the group, not
// a bare "context canceled".
func (f *deviceFuture) await(ctx context.Context) (device.Device, error) {
	select {
	case dev := <-f.ch:
		return dev, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// closeUnclaimed releases a device that connected but was never awaited.
// Call it only after the errgroup has been waited: the connect goroutine has
// finished, so the channel holds the device if it connected.
func (f *deviceFuture) closeUnclaimed() {
	select {
	case dev := <-f.ch:
		_ = dev.Close()
	default:
	}
}
