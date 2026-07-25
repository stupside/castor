package core

import (
	"context"

	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/media"
)

// ResolveDelivery decides how the renderer receives the stream: pass-through
// (the device fetches the source URL itself) when it both self-fetches AND
// already accepts the source container, so there is nothing to rewrap; otherwise
// castor serves a locally produced stream. It is decided purely from capability
// data: a self-fetching renderer hands an accepted container straight to the
// device and has the rest remuxed, while a push-only renderer always serves.
func ResolveDelivery(caps media.Renderer, source *media.Stream) DeliveryMode {
	if caps.SelfFetch && caps.AcceptsContainer(source.ContentType) {
		return DeliverPassthrough
	}
	return DeliverServe
}

// ResolveSubtitle decides the subtitle axis from the renderer and config. Stage 1
// has one active mechanism, whisper burn-in, chosen exactly when the transcriber
// is enabled AND the renderer does not fetch its own bytes. A self-fetching
// renderer either passes the source through or remuxes it, and in neither case is
// there a drawtext encode to draw cues into (it takes captions as a native track
// in Stage 2); a push-only renderer always serves a local encode, which is
// exactly what burn-in needs. A future SubtitleSidecar will branch here on the
// renderer's advertised caption support.
func ResolveSubtitle(caps media.Renderer, cfg Config) SubtitleMode {
	if cfg.Whisper.Enable && !caps.SelfFetch {
		return SubtitleBurnIn
	}
	return SubtitleOff
}

// ResolveVideo fills opts' video fields from the renderer's advertised support
// and the source's probed track: the copy-vs-encode decision the served path
// makes once it holds a probe (the read-once spool probe, or the network-remux
// source probe). It is the video counterpart to ResolveAudio and, like it, a
// mutator over opts that queries capability methods:
//
//   - copy: no subtitles to burn, the source fits under the height ceiling, and
//     the renderer decodes the source envelope natively: the bitstream passes
//     through untouched (VideoEncoder nil), no quality loss;
//   - re-encode: otherwise, to the most efficient codec the renderer advertises
//     and this host can hardware-encode (HEVC at half the bitrate, else H.264),
//     bounded by that codec's VBV-capped target.
//
// A subtitle burn-in always forces the re-encode: drawtext needs decoded frames,
// so a copied bitstream cannot carry cues. The signal is opts.SubtitleTextFile,
// which the caller wires before calling this (the coupling EncodeArgs documents),
// so the decision stays a function of opts.
func ResolveVideo(ctx context.Context, opts *ffmpeg.EncodeOptions, caps media.Renderer, src media.ProbeInfo, cfg Config) {
	hasSubs := opts.SubtitleTextFile != ""
	if !hasSubs && withinMaxHeight(src, cfg.Resolver.MaxHeight) && caps.CanCopyVideo(src) {
		// Copy the video bitstream untouched.
		opts.VideoEncoder = nil
		return
	}

	// Re-encode. Pick the most efficient codec the renderer advertises and this
	// host can encode in hardware (HEVC at half the bitrate, else H.264), then
	// apply that codec's VBV-capped bitrate target. SelectEncoder proves an
	// encoder with a real test encode (cached per process), so it takes ctx: a
	// slow or wedged probe is cancelled when the cast's context ends rather than
	// running detached, exactly as the old inline code passed its errgroup ctx.
	enc := selectVideoEncoder(caps, func(c media.Codec) (ffmpeg.Encoder, bool) {
		return ffmpeg.SelectEncoder(ctx, cfg.Transcode.FFmpegPath, c)
	})
	opts.VideoEncoder = &enc
	t := videoTargets[enc.Codec]
	opts.VideoBitrate, opts.VideoMaxrate, opts.VideoBufsize = t.bitrate, t.maxrate, t.bufsize
}

// codecPreference ranks re-encode target codecs by efficiency, most efficient
// first. selectVideoEncoder picks the first one the renderer decodes and this
// host can hardware-encode; H.264 is last and always resolves to at least a
// software baseline, so selection never fails. Adding a codec is one entry here.
var codecPreference = []media.Codec{media.CodecHEVC, media.CodecH264}

// selectVideoEncoder chooses the encoder for a re-encode: the most efficient
// codec both the renderer advertises and this host can produce. A codec above
// H.264 is taken only when a hardware encoder backs it, since software HEVC
// cannot hold realtime at 1080p; H.264 is the floor and accepts its software
// baseline. selectEncoder resolves an encoder for a codec (ffmpeg.SelectEncoder
// in production, a fake in tests); its ok is false for a codec with no encoder.
func selectVideoEncoder(caps media.Renderer, selectEncoder func(media.Codec) (ffmpeg.Encoder, bool)) ffmpeg.Encoder {
	for _, codec := range codecPreference {
		if !caps.SupportsCodec(codec) {
			continue
		}
		if enc, ok := selectEncoder(codec); ok && (enc.Hardware || codec == media.CodecH264) {
			return enc
		}
	}
	// The renderer advertised nothing we can encode to (or only a non-H.264
	// codec with no hardware here); fall back to the universal H.264 baseline.
	enc, _ := selectEncoder(media.CodecH264)
	return enc
}

// withinMaxHeight reports whether a probed source fits under the configured cast
// height ceiling. An unknown height (0) passes: the source is trusted rather
// than force-transcoded on missing metadata. A source above the cap is not
// copy-eligible, so it falls through to a transcode that scales it down.
func withinMaxHeight(src media.ProbeInfo, maxHeight int) bool {
	return src.VideoHeight == 0 || src.VideoHeight <= maxHeight
}

// videoTarget is a VBV-capped bitrate: the average, the peak cap, and the buffer
// window the cap applies over.
type videoTarget struct{ bitrate, maxrate, bufsize string }

// videoTargets is the re-encode target per codec, bounding the transcoder's
// output so it stays within the renderer's decode budget. HEVC needs about half
// of H.264 for the same quality. maxrate == bitrate makes the VBV cap a true
// ceiling rather than an average the encoder overshoots; bufsize is ~2s. They
// apply only when ResolveVideo re-encodes; a stream-copy never reads them.
// Adding a codec the pipeline encodes to is one entry here.
var videoTargets = map[media.Codec]videoTarget{
	media.CodecH264: {bitrate: "4M", maxrate: "4M", bufsize: "8M"},
	media.CodecHEVC: {bitrate: "2M", maxrate: "2M", bufsize: "4M"},
}
