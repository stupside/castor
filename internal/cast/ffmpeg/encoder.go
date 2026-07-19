package ffmpeg

import (
	"context"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stupside/castor/internal/media"
)

// Encoder is one concrete way to produce a codec on this host: the ffmpeg -c:v
// name plus the command fragments it contributes. EncodeArgs splices those
// fragments in verbatim and never special-cases an encoder, so supporting a new
// codec or backend is a registry entry, not new control flow. A nil *Encoder in
// EncodeOptions means stream-copy.
type Encoder struct {
	Name     string      // -c:v value, e.g. "libx264", "hevc_videotoolbox"
	Codec    media.Codec // the abstract codec produced, independent of the name
	Hardware bool        // GPU-backed: trusted only after a real test encode
	InitArgs []string    // emitted before the input: hardware device setup
	Filters  []string    // appended to the -vf chain: e.g. the GPU upload
	Flags    []string    // encoder-specific -c:v flags: preset, pix_fmt, GOP
}

const vaapiRenderNode = "/dev/dri/renderD128"

// Shared fragment recipes, so a codec's software/VideoToolbox/VA-API variants
// differ only by encoder name. -pix_fmt yuv420p is a delivery invariant on the
// CPU-frame encoders: without it a 10-bit source yields a High 10 / Main 10
// stream most renderers can't decode. VideoToolbox additionally needs -g to lift
// its sub-second default GOP so the planner's force_key_frames sets the real
// cadence. VA-API downconverts through its format=nv12 upload filter instead, so
// it must not carry -pix_fmt (its encoder input is a GPU surface).
var (
	// veryfast keeps the software encoders ahead of realtime in the live pipeline.
	softwareFlags     = []string{"-preset", "veryfast", "-pix_fmt", "yuv420p"}
	videotoolboxFlags = []string{"-pix_fmt", "yuv420p", "-g", "600"}
	vaapiInit         = []string{"-init_hw_device", "vaapi=va:" + vaapiRenderNode, "-filter_hw_device", "va"}
	vaapiFilters      = []string{"format=nv12", "hwupload"}
)

// The encoders. Platform is not assumed: every backend is a candidate, and
// SelectEncoder discovers which ones actually work here by running a real test
// encode (VA-API on macOS, or VideoToolbox on Linux, simply fails that probe and
// is skipped). That keeps the set agnostic and unit-testable on any OS.
var (
	libx264 = Encoder{Name: "libx264", Codec: media.CodecH264, Flags: softwareFlags}
	libx265 = Encoder{Name: "libx265", Codec: media.CodecHEVC, Flags: slices.Concat(softwareFlags, []string{"-x265-params", "log-level=error"})}

	h264VideoToolbox = Encoder{Name: "h264_videotoolbox", Codec: media.CodecH264, Hardware: true, Flags: videotoolboxFlags}
	hevcVideoToolbox = Encoder{Name: "hevc_videotoolbox", Codec: media.CodecHEVC, Hardware: true, Flags: videotoolboxFlags}

	h264VAAPI = Encoder{Name: "h264_vaapi", Codec: media.CodecH264, Hardware: true, InitArgs: vaapiInit, Filters: vaapiFilters}
	hevcVAAPI = Encoder{Name: "hevc_vaapi", Codec: media.CodecHEVC, Hardware: true, InitArgs: vaapiInit, Filters: vaapiFilters}
)

// registry lists every encoder, grouped by codec with hardware candidates ahead
// of the software baseline. SelectEncoder tries the hardware ones first (each
// gated by testEncode) and falls back to the always-available baseline.
var registry = []Encoder{
	h264VideoToolbox, h264VAAPI, libx264,
	hevcVideoToolbox, hevcVAAPI, libx265,
}

// SelectEncoder returns the best working encoder for codec on this host: a
// hardware encoder whose real test encode passes, otherwise the software
// baseline. ok is false only for a codec with no registered encoder at all;
// availability is cached, so repeat calls are cheap.
func SelectEncoder(ctx context.Context, ffmpegPath string, codec media.Codec) (enc Encoder, ok bool) {
	for _, e := range registry {
		if e.Codec == codec && e.Hardware && available(ctx, ffmpegPath, e) {
			slog.InfoContext(ctx, "hardware encoder selected", "encoder", e.Name, "codec", string(codec))
			return e, true
		}
	}
	for _, e := range registry {
		if e.Codec == codec && !e.Hardware {
			slog.InfoContext(ctx, "software encoder selected", "encoder", e.Name, "codec", string(codec))
			return e, true
		}
	}
	return Encoder{}, false
}

// availability caches each encoder's test-encode result for the process: a
// working GPU is proven once, and a wedged one isn't retried per cast.
var (
	availMu    sync.Mutex
	availCache = map[string]bool{}
)

func available(ctx context.Context, ffmpegPath string, e Encoder) bool {
	availMu.Lock()
	defer availMu.Unlock()
	if v, cached := availCache[e.Name]; cached {
		return v
	}
	ok := testEncode(ctx, ffmpegPath, e)
	availCache[e.Name] = ok
	if !ok {
		slog.InfoContext(ctx, "hardware encoder unavailable; falling back to software", "encoder", e.Name)
	}
	return ok
}

// testEncode reports whether a one-frame encode through e exits cleanly, the
// only reliable proof a listed encoder truly works on the hardware here
// (h264_vaapi is listed on any VA-API build regardless of the GPU). It reuses
// e's own device setup and filters so the probe matches the real command.
func testEncode(ctx context.Context, ffmpegPath string, e Encoder) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := append([]string{"-hide_banner"}, e.InitArgs...)
	args = append(args, "-f", "lavfi", "-i", "testsrc2=size=256x144:rate=25:duration=0.1")
	if len(e.Filters) > 0 {
		args = append(args, "-vf", strings.Join(e.Filters, ","))
	}
	args = append(args, "-c:v", e.Name, "-f", "null", "-")
	return exec.CommandContext(ctx, ffmpegPath, args...).Run() == nil
}
