package cast

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/cast/spool"
	"github.com/stupside/castor/internal/cast/whisper"
)

const (
	// transcriptionLeadSeconds is how far ahead of the encoder whisper must
	// be before playback starts. The encoder is pinned to realtime after an
	// initial burst while the pull feeds whisper at up to 2x, so once this
	// gate opens the lead only grows. The margin past the burst covers the
	// streaming policy's commit lag: LocalAgreement holds words back until a
	// second hypothesis confirms them.
	transcriptionLeadSeconds = ffmpeg.EncodeReadrateBurstSeconds + 10

	// gateStallTimeout aborts the playback gate when the upstream stops
	// delivering bytes entirely. ffmpeg's own -rw_timeout/-reconnect handle
	// transient drops; this catches the CDN that keeps the connection open
	// but sends nothing (throttled or burned token), which would otherwise
	// hang the gate in silence forever.
	gateStallTimeout = 60 * time.Second
)

// waitForPlayable blocks until the pipeline can start encoding: whisper has
// built up its lead (or finished — short sources), or, without subtitles,
// the spool holds data. Aborts early if the pull dies before producing
// anything playable, or if the upstream stalls outright. Progress is logged
// every few seconds so a slow start is never a silent one.
func waitForPlayable(ctx context.Context, tr *whisper.Transcriber, sp *spool.Spool, pl *pull) error {
	start := time.Now()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	lastSize := int64(-1)
	lastGrowth := time.Now()
	var lastLog time.Time
	for {
		pullDone := false
		select {
		case <-pl.Done():
			pullDone = true
			// Any pull failure before playback starts is fatal: casting
			// whatever fragment made it into the spool would play a few
			// seconds and stop mid-scene, which reads as a worse failure
			// than a clear error. (After playback starts, the spool keeps
			// serving and a pull error only truncates the tail.)
			if err := pl.Err(); err != nil {
				return fmt.Errorf("upstream pull failed before playback (spooled %d bytes): %w", sp.Size(), err)
			}
		default:
		}

		size := sp.Size()
		if size != lastSize {
			lastSize = size
			lastGrowth = time.Now()
		} else if !pullDone && time.Since(lastGrowth) > gateStallTimeout {
			// Surface ffmpeg's stderr to name the failure. The common case is a
			// 404 storm — "Failed to open segment", "HTTP error 404" — meaning
			// the source handed us a signed playlist whose segments have already
			// expired (dead for everyone, not just us); re-extracting gets a
			// fresh token. Empty stderr means ffmpeg connected but no bytes and
			// no warnings arrived: a genuinely silent upstream. Without dumping
			// this, the gate kills ffmpeg via ctx cancellation and the puller's
			// own error path never gets to log these lines.
			if lines := pl.StderrTail(); len(lines) > 0 {
				for _, line := range lines {
					slog.WarnContext(ctx, "puller ffmpeg stderr", "line", line)
				}
			} else {
				slog.WarnContext(ctx, "puller emitted no stderr — connection accepted but zero bytes and no warnings delivered (silent upstream)")
			}
			return fmt.Errorf("upstream stalled before playback: no data for %s (spooled %d bytes) — the source playlist is likely expired (segments 404); try casting again to get a fresh link",
				gateStallTimeout, size)
		}

		ready := false
		if tr != nil {
			// Spool bytes are required too: a pull that dies instantly
			// flips tr.Done() (PCM hits EOF) before its error lands, and
			// the gate must not open onto an empty spool in that window.
			// A dead upstream can't spin here forever — once Done() is
			// closed the pull-error branch above returns.
			ready = size > 0 && (tr.LatestEnd() >= transcriptionLeadSeconds || tr.Done())
		} else {
			ready = size > 0 || pullDone
		}
		if ready {
			slog.InfoContext(ctx, "playback gate open",
				"waited", time.Since(start).Round(time.Millisecond),
				"spooled_bytes", size,
				"transcribed_lead_seconds", int(leadSeconds(tr)),
			)
			return nil
		}

		if time.Since(lastLog) >= 5*time.Second {
			slog.InfoContext(ctx, "waiting for playback gate",
				"spooled_bytes", size,
				"transcribed_lead_seconds", int(leadSeconds(tr)),
				"need_lead_seconds", transcriptionLeadSeconds,
			)
			lastLog = time.Now()
		}

		select {
		case <-ctx.Done():
			// context.Cause surfaces the real reason when a concurrent step
			// (e.g. device discovery) cancelled the group, not a bare
			// "context canceled".
			return context.Cause(ctx)
		case <-tick.C:
		}
	}
}

func leadSeconds(tr *whisper.Transcriber) float64 {
	if tr == nil {
		return 0
	}
	return tr.LatestEnd()
}
