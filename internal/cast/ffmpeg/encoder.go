package ffmpeg

import (
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// Encoder is a video encoder: the ffmpeg -c:v value plus the fragments it adds
// to the command. Software encoders leave InitArgs and Filters empty; a
// hardware encoder fills in its GPU device setup and upload filter. A nil
// *Encoder in EncodeOptions means stream-copy (no re-encode).
type Encoder struct {
	Name     string   // -c:v value, e.g. "libx264", "h264_vaapi"
	InitArgs []string // emitted before the input: hardware device setup
	Filters  []string // appended to the -vf chain: the GPU upload
	Flags    []string // encoder-specific -c:v flags, e.g. a preset
}

const vaapiRenderNode = "/dev/dri/renderD128"

// The encoders. Defined here (not in the build-tagged files) so their shape is
// unit-testable on any OS; which hardware one is a candidate is decided per
// GOOS by hardwareH264. veryfast keeps libx264 ahead of realtime in the live
// pipeline.
var (
	libx264   = Encoder{Name: "libx264", Flags: []string{"-preset", "veryfast"}}
	h264VAAPI = Encoder{
		Name:     "h264_vaapi",
		InitArgs: []string{"-init_hw_device", "vaapi=va:" + vaapiRenderNode, "-filter_hw_device", "va"},
		Filters:  []string{"format=nv12", "hwupload"},
	}
	h264VideoToolbox = Encoder{Name: "h264_videotoolbox"}
)

// SelectH264Encoder returns a hardware H.264 encoder if one actually works on
// this host (proven by a real test encode), else libx264.
func SelectH264Encoder(ctx context.Context, ffmpegPath string) Encoder {
	if e, ok := hardwareH264(ctx, ffmpegPath); ok {
		slog.InfoContext(ctx, "hardware encoder selected", "encoder", e.Name)
		return e
	}
	slog.InfoContext(ctx, "software encoder selected", "encoder", libx264.Name)
	return libx264
}

// testEncode reports whether a one-frame encode through e exits cleanly, the
// only proof a listed encoder truly works on the hardware. It reuses e's own
// fragments so the probe matches the real command.
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
