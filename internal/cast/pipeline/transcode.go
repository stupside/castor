package pipeline

import (
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
// changes). muxer is the ffmpeg muxer for the plan's output container, derived
// from the renderer's declared ServedContainer. Audio is filled by
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
