// Package pipeline is the cast executor: it connects the renderer and runs
// exactly the stages a pure core.Plan names, over device-blind building blocks
// (pull, gate, transcode, serve). It replaces the old per-device strategies.
//
// The one thing a device family shapes here is WHEN the renderer is connected,
// and only its self-fetch bit decides that (device.SelfFetches, a static fact
// known before discovery), not a per-device branch:
//   - a self-fetching renderer might pass the source URL through, which needs its
//     negotiated capabilities, so it is connected first (its discovery is fast);
//   - a non-self-fetching renderer can only play what castor serves it, so its
//     cast is always a read-once spool no matter what it advertises. It begins
//     buffering the single-use source immediately and connects concurrently, so
//     slow SSDP discovery does not age a short-lived signed URL before the pull.
//
// Three compositions cover both of today's device families exactly:
//   - pass-through: hand the device the source URL, it fetches the bytes itself;
//   - network remux: a self-fetching renderer that rejects the source container,
//     so a single ffmpeg reads the source URL and remuxes to a container it takes;
//   - read-once spool: a served renderer that never self-fetches, so one puller
//     buffers the single-use source into a local spool the encoder reads from,
//     with optional whisper burn-in. This is the concurrent-connect path.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sync/errgroup"

	"github.com/stupside/castor/internal/cast/core"
	"github.com/stupside/castor/internal/cast/deliver/spool"
	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/cast/subtitle/whisper"
	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/media"
)

// ConnectFunc discovers and connects the configured renderer. Production passes
// core.Connect; tests inject a fake so Run can be driven without a live network.
type ConnectFunc func(context.Context, core.Config) (device.Device, error)

// Run casts source to the configured renderer. It is the single entry point that
// replaced both per-device strategies. The only device-family influence is the
// connect timing, keyed on the static device.SelfFetches bit: a self-fetching
// renderer is connected up front (it may pass through, which needs its caps); a
// non-self-fetching one always serves a spool, so it pulls immediately and
// connects concurrently. Run owns the connected device and closes it.
func Run(ctx context.Context, cfg core.Config, connect ConnectFunc, source *media.Stream, localIP string) error {
	if device.SelfFetches(cfg.Device.Type) {
		return runSelfFetch(ctx, cfg, connect, source, localIP)
	}
	return runSpooled(ctx, cfg, connect, source, localIP)
}

// runSelfFetch connects a smart renderer up front (its discovery is fast) and
// then, per the plan its live capabilities produce, either hands it the source
// URL (it accepts the container) or remuxes to a container it will take.
func runSelfFetch(ctx context.Context, cfg core.Config, connect ConnectFunc, source *media.Stream, localIP string) error {
	dev, err := connect(ctx, cfg)
	if err != nil {
		return err
	}
	defer dev.Close()

	plan := core.NewPlan(source, dev.Capabilities(), cfg)
	if plan.Delivery == core.DeliverPassthrough {
		return passthrough(ctx, dev, source)
	}
	return runRemux(ctx, cfg, plan, dev, source, localIP)
}

// passthrough hands the device the source URL and lets it fetch the bytes
// directly. The renderer's own buffering handles pacing; castor touches none of
// the media, so there is nothing to encode and no subtitles to burn (the plan
// already forced SubtitleOff on a pass-through).
func passthrough(ctx context.Context, dev device.Device, source *media.Stream) error {
	slog.InfoContext(ctx, "execution plan", "delivery", "passthrough", "content_type", source.ContentType)
	slog.InfoContext(ctx, "starting playback", "url", source.URL.String(), "content_type", source.ContentType)
	if err := dev.Play(ctx, source.URL, source.ContentType); err != nil {
		return fmt.Errorf("starting playback: %w", err)
	}
	slog.InfoContext(ctx, "playback handed off to device")
	return nil
}

// runRemux is the single-ffmpeg served path for a self-fetching renderer that
// rejects the source container: network input, video stream-copied, container
// changed to the plan's output type. No whisper, no input spool. A single-file
// remux is spooled by the replay server so the device can replay from 0; a
// segmented (HLS) remux is packaged into a directory the HLS server fronts.
func runRemux(ctx context.Context, cfg core.Config, plan core.Plan, dev device.Device, source *media.Stream, localIP string) error {
	slog.InfoContext(ctx, "execution plan", "delivery", "remux", "output_content_type", plan.OutputContentType)

	workDir, err := os.MkdirTemp("", "castor-")
	if err != nil {
		return fmt.Errorf("creating work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	fmtInfo, ok := media.FormatForContentType(plan.OutputContentType)
	if !ok {
		return fmt.Errorf("no format for output content type %q", plan.OutputContentType)
	}
	opts := remuxNetworkOptions(source, cfg.Transcode.RWTimeout, fmtInfo.Muxer)

	// Resolve audio from a source probe, the same decision the spool path makes
	// off its spool. This path has no input spool, so it reads the upstream
	// directly: one extra fetch before the remux ffmpeg opens the same URL again.
	// For the usual remux input (a static file in a container the device won't
	// take, e.g. AVI) that is fine. It is NOT free for a short-lived or single-use
	// signed link: the probe can burn the token or a rate-limit slot and leave the
	// remux ffmpeg with a 403/429. HLS (the common signed-link shape) normally
	// passes through and never reaches here, but only while the receiver advertises
	// HLS, so this is a known, accepted risk, not a guarantee. A failed probe
	// leaves srcInfo zero, which core.ResolveAudio degrades to stereo AAC.
	srcInfo, err := ffmpeg.Probe(ctx, cfg.Resolver.FFprobePath, source.URL.String(), source.Headers)
	if err != nil {
		slog.WarnContext(ctx, "source probe failed; will re-encode audio to stereo AAC", "error", err)
	}
	core.ResolveAudio(&opts, dev.Capabilities(), srcInfo)
	slog.InfoContext(ctx, "remux audio decision",
		"audio_codec", opts.AudioCodec,
		"source_audio_codec", string(srcInfo.AudioCodec),
		"source_audio_channels", srcInfo.AudioChannels,
	)

	// The delivery mechanism (replay-from-zero stream vs live HLS directory) is
	// selected by fmtInfo.Delivery inside core.Serve, not here; this path just
	// hands it the encode and where to serve from.
	return core.Serve(ctx, dev, core.OpenParams{
		FFmpegPath: cfg.Transcode.FFmpegPath,
		Opts:       opts,
		LocalIP:    localIP,
		WorkDir:    workDir,
		Format:     fmtInfo,
	})
}

// runSpooled is the read-once cast for a renderer that does not self-fetch:
// puller into spool (+ PCM into whisper) into tail into encoder (drawtext burn-in)
// into replay server into device. Each stage lives in its own file; this function
// only wires them together, connecting the renderer concurrently with the pull.
//
// Because the renderer does not self-fetch, the cast is a served spool regardless
// of its advertised caps, so the plan is fixed from a known-false SelfFetch and
// only the copy-vs-encode step needs the negotiated capabilities. Connect runs in
// the errgroup and is awaited only after the gate, so slow discovery overlaps the
// pull instead of aging the signed source URL ahead of it.
//
// Lifecycle: a cancellable context feeds an errgroup that owns the concurrent
// stages (the connect, transcription, and the cue writer). Defer order is LIFO:
// cancel runs first (signals every goroutine and kills both ffmpeg processes),
// g.Wait blocks until they have unwound, a connected-but-unclaimed device is
// closed, and only then is the work directory removed, so no goroutine is still
// writing a cue file into a directory being deleted.
func runSpooled(parentCtx context.Context, cfg core.Config, connect ConnectFunc, source *media.Stream, localIP string) error {
	// The read-once spool serves MPEG-TS: the spool is strictly append-only so a
	// tail can read it while it grows and the replay server can hand every client
	// the stream from byte 0. That is a property of this delivery mechanism, not of
	// any renderer, so the container is declared here rather than assumed in core.
	plan := core.NewPlan(source, media.Renderer{SelfFetch: false, ServedContainer: media.MPEGTS}, cfg)
	slog.InfoContext(parentCtx, "execution plan",
		"delivery", "spool",
		"output_content_type", plan.OutputContentType,
		"subtitles", plan.Subtitle == core.SubtitleBurnIn,
	)

	workDir, err := os.MkdirTemp("", "castor-")
	if err != nil {
		return fmt.Errorf("creating work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }() // registered first, runs last

	dev := newDeviceFuture()
	defer dev.closeUnclaimed()

	runCtx, cancel := context.WithCancel(parentCtx)
	g, ctx := errgroup.WithContext(runCtx)
	defer func() { _ = g.Wait() }()
	defer cancel()

	// Discover and connect the renderer concurrently with everything below. It is
	// not needed until the copy-vs-encode decision, and SSDP discovery plus connect
	// can take seconds, seconds the short-lived signed source URL cannot spare.
	dev.connect(ctx, g, connect, cfg)

	sp, err := spool.New(filepath.Join(workDir, "spool.ts"))
	if err != nil {
		return err
	}

	// The subtitle decision came from the plan (SubtitleBurnIn only when whisper is
	// enabled and this served renderer cannot take a native track); newSubtitles
	// then still downgrades to nil if the whisper model fails to init, so wantPCM
	// tracks the real, live stage.
	var subs *subtitles
	if plan.Subtitle == core.SubtitleBurnIn {
		subs = newSubtitles(ctx, cfg.Whisper, workDir)
	}

	pl, err := startPull(ctx, cfg.Transcode, source, sp, subs != nil)
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
	// copy-vs-encode decision below. Discovery and connect have been running since
	// the top and overlap the whole pull+gate window, so this await almost never
	// blocks; if connect failed, ctx carries the cause.
	d, err := dev.await(ctx)
	if err != nil {
		return err
	}
	defer d.Close()

	// The spool now holds real bytes. Probe it locally (no upstream round-trip) to
	// decide whether the source video can be stream-copied into MPEG-TS or must be
	// re-encoded. A failed or partial probe leaves srcInfo zero, which CanCopyVideo
	// rejects, i.e. it falls back to a transcode.
	srcInfo, err := ffmpeg.Probe(ctx, cfg.Resolver.FFprobePath, sp.Path(), nil)
	if err != nil {
		slog.WarnContext(ctx, "spool probe failed; will re-encode video", "error", err)
	}

	opts := spoolEncodeOptions(cfg.Resolver.MaxHeight)
	caps := d.Capabilities()

	// Audio is resolved independently of the video decision: it can be copied
	// (preserving 5.1/7.1) even when the video must be re-encoded, and vice versa.
	core.ResolveAudio(&opts, caps, srcInfo)

	// Wire the burn-in BEFORE ResolveVideo: attach sets opts.SubtitleTextFile,
	// which is the signal ResolveVideo reads to force a re-encode (drawtext needs
	// decoded frames, so a copied bitstream cannot carry cues). The cue file must
	// also exist before ffmpeg starts or drawtext's filter init fails.
	if subs != nil {
		if err := subs.attach(&opts); err != nil {
			return err
		}
	}

	// Copy-vs-encode against the spool probe: stream-copy when nothing forces a
	// re-encode (no burn-in, within the height ceiling, renderer decodes the
	// envelope), else the most efficient advertised + hardware codec at its
	// VBV-capped target. Identical decision to the old inline per-device logic. ctx
	// is threaded so a wedged encoder test-probe unwinds with the cast.
	core.ResolveVideo(ctx, &opts, caps, srcInfo, cfg)

	videoCodec := ffmpeg.CodecCopy
	if opts.VideoEncoder != nil {
		videoCodec = opts.VideoEncoder.Name
	}
	slog.InfoContext(ctx, "spool encode decision",
		"video_codec", videoCodec,
		"source_codec", string(srcInfo.VideoCodec),
		"source_profile", srcInfo.VideoProfile,
		"source_height", srcInfo.VideoHeight,
		"audio_codec", opts.AudioCodec,
		"source_audio_codec", string(srcInfo.AudioCodec),
		"source_audio_channels", srcInfo.AudioChannels,
		"subtitles", subs != nil,
	)

	tail, err := sp.Tail(ctx)
	if err != nil {
		return err
	}
	defer tail.Close()

	startOpts := []ffmpeg.StartOption{ffmpeg.WithStdin(tail)}
	if subs != nil {
		startOpts = append(startOpts, ffmpeg.WithExtraPipe()) // -progress on fd 3
	}

	fmtInfo, ok := media.FormatForContentType(plan.OutputContentType)
	if !ok {
		return fmt.Errorf("no format for output content type %q", plan.OutputContentType)
	}

	// core.Serve owns the encoder; OnStarted wires the burn-in follower to the
	// encoder's -progress pipe (fd 3) as soon as it starts, keeping the whisper
	// errgroup coupling here in the pipeline instead of in core.
	return core.Serve(ctx, d, core.OpenParams{
		FFmpegPath: cfg.Transcode.FFmpegPath,
		Opts:       opts,
		StartOpts:  startOpts,
		LocalIP:    localIP,
		WorkDir:    workDir,
		Format:     fmtInfo,
		OnStarted: func(proc *ffmpeg.Process) {
			if subs != nil && proc.Extra != nil {
				subs.follow(ctx, g, proc.Extra)
			}
		},
	})
}

// deviceFuture is the async renderer connection. The connect goroutine runs in
// the spool pipeline's errgroup, so a connect failure cancels the group with that
// error as the cause, and the result waits in a one-slot channel until awaited. A
// device connected but never claimed (the gate failed first) is closed by
// closeUnclaimed during teardown.
type deviceFuture struct {
	ch chan device.Device
}

func newDeviceFuture() *deviceFuture {
	return &deviceFuture{ch: make(chan device.Device, 1)}
}

func (f *deviceFuture) connect(ctx context.Context, g *errgroup.Group, connect ConnectFunc, cfg core.Config) {
	g.Go(func() error {
		dev, err := connect(ctx, cfg)
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

// await blocks until the device is connected or ctx ends. context.Cause surfaces
// the real reason when a concurrent stage cancelled the group, not a bare
// "context canceled".
func (f *deviceFuture) await(ctx context.Context) (device.Device, error) {
	select {
	case dev := <-f.ch:
		return dev, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// closeUnclaimed releases a device that connected but was never awaited. Call it
// only after the errgroup has been waited: the connect goroutine has finished, so
// the channel holds the device if it connected.
func (f *deviceFuture) closeUnclaimed() {
	select {
	case dev := <-f.ch:
		_ = dev.Close()
	default:
	}
}
