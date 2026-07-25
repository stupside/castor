package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/media"
)

// keyframeSeconds caps the encoded GOP so a renderer joining mid-stream resyncs
// within a couple seconds. It applies only to the served spool encode; the
// network remux stream-copies video and keeps the source's own keyframes.
const keyframeSeconds = 2

// spoolEncodeOptions is the base encode invocation for the served spool path:
// MPEG-TS out, read from the local spool over stdin (PipeFormat), height-capped,
// and GOP-bounded. ResolveVideo/ResolveAudio and the burn-in attach fill in the
// codec-specific fields against the spool probe before the process starts.
func spoolEncodeOptions(maxHeight int) ffmpeg.EncodeOptions {
	return ffmpeg.EncodeOptions{
		OutputFormat:        "mpegts",
		PipeFormat:          "mpegts",
		VideoMaxHeight:      maxHeight,
		KeyframeIntervalSec: keyframeSeconds,
	}
}

// remuxNetworkOptions is the base encode invocation for the network remux path
// (a self-fetching renderer that rejects the source container): the source wired
// for a direct network read and no video encoder, so the bitstream is
// stream-copied (the renderer decodes the source codec, only the container
// changes). muxer is the ffmpeg muxer for the plan's output container ("mp4" for
// Chromecast's fragmented mp4, "hls" for Roku's live HLS). Audio is filled by
// core.ResolveAudio afterward against a source probe.
func remuxNetworkOptions(source *media.Stream, rwTimeout time.Duration, muxer string) ffmpeg.EncodeOptions {
	return ffmpeg.EncodeOptions{
		OutputFormat:      muxer,
		SourceURL:         source.URL,
		SourceHeaders:     source.Headers,
		SourceContentType: source.ContentType,
		RWTimeoutMicros:   rwTimeout.Microseconds(),
	}
}

// startTranscode launches the output ffmpeg for a served cast from fully
// resolved options. Both served paths share it: only their EncodeOptions and
// stdin wiring differ (the spool path feeds the local tail on stdin and routes
// -progress to fd 3 for burn-in, the network path opens the source URL itself),
// which the callers pass as start options. core.FinishEncoder tears it down.
func startTranscode(ctx context.Context, ffmpegPath string, opts ffmpeg.EncodeOptions, startOpts ...ffmpeg.StartOption) (*ffmpeg.Process, error) {
	proc, err := ffmpeg.Start(ctx, ffmpegPath, ffmpeg.EncodeArgs(opts), startOpts...)
	if err != nil {
		return nil, fmt.Errorf("starting transcode: %w", err)
	}
	return proc, nil
}
