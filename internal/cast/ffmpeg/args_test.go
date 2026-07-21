package ffmpeg

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stupside/castor/internal/media"
)

// argValue returns the token immediately after the last occurrence of flag, or
// "" if the flag is absent. ffmpeg options are flag/value pairs, so this reads
// the value the encoder will actually receive.
func argValue(args []string, flag string) string {
	for i := len(args) - 2; i >= 0; i-- {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func hasFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

func TestEncodeArgsCopyRemux(t *testing.T) {
	// A nil VideoEncoder stream-copies the video.
	args := EncodeArgs(EncodeOptions{
		PipeFormat:      "mpegts",
		OutputFormat:    "mpegts",
		AudioCodec:      "aac",
		AudioBitrate:    "256k",
		AudioSampleRate: 48000,
		AudioChannels:   2,
	})

	if got := argValue(args, "-c:v"); got != "copy" {
		t.Fatalf("video codec = %q, want copy", got)
	}
	if hasFlag(args, "-preset") {
		t.Error("copy must not set -preset")
	}
	if hasFlag(args, "-b:v") {
		t.Error("copy must not set -b:v")
	}
	if hasFlag(args, "-vf") {
		t.Error("copy must not scale (no -vf)")
	}
	if hasFlag(args, "-init_hw_device") {
		t.Error("copy must not initialise a hardware device")
	}
	// A copied bitstream can't be re-rate-controlled, re-formatted, or
	// re-keyframed: those flags target the encoder, which isn't running.
	for _, f := range []string{"-maxrate", "-bufsize", "-pix_fmt", "-force_key_frames"} {
		if hasFlag(args, f) {
			t.Errorf("copy must not set %s", f)
		}
	}
	if got := argValue(args, "-c:a"); got != "aac" {
		t.Errorf("audio codec = %q, want aac", got)
	}
}

func TestEncodeArgsReadrateHeadroom(t *testing.T) {
	// Burning subtitles paces the encode with -readrate. It must be just above
	// realtime (EncodeReadrate), not 1.0: at dead-even playback speed the
	// renderer's buffer has no headroom to rebuild after jitter and rebuffers.
	args := EncodeArgs(EncodeOptions{
		PipeFormat:       "mpegts",
		OutputFormat:     "mpegts",
		VideoEncoder:     &libx264,
		SubtitleTextFile: "/tmp/cue.txt",
		AudioCodec:       "aac",
	})
	if got := argValue(args, "-readrate"); got != EncodeReadrate {
		t.Errorf("readrate = %q, want %q (headroom above realtime)", got, EncodeReadrate)
	}
}

func TestEncodeArgsLibx264(t *testing.T) {
	args := EncodeArgs(EncodeOptions{
		PipeFormat:          "mpegts",
		OutputFormat:        "mpegts",
		VideoEncoder:        &libx264,
		VideoBitrate:        "4M",
		VideoMaxrate:        "4M",
		VideoBufsize:        "8M",
		VideoMaxHeight:      1080,
		KeyframeIntervalSec: 2,
		AudioCodec:          "aac",
	})

	if got := argValue(args, "-c:v"); got != "libx264" {
		t.Fatalf("video codec = %q, want libx264", got)
	}
	if got := argValue(args, "-preset"); got != "veryfast" {
		t.Errorf("preset = %q, want veryfast", got)
	}
	if got := argValue(args, "-b:v"); got != "4M" {
		t.Errorf("bitrate = %q, want 4M", got)
	}
	if got := argValue(args, "-maxrate"); got != "4M" {
		t.Errorf("maxrate = %q, want 4M (VBV cap)", got)
	}
	if got := argValue(args, "-bufsize"); got != "8M" {
		t.Errorf("bufsize = %q, want 8M (VBV cap)", got)
	}
	if got := argValue(args, "-pix_fmt"); got != "yuv420p" {
		t.Errorf("pix_fmt = %q, want yuv420p (8-bit, Samsung-decodable)", got)
	}
	if got := argValue(args, "-force_key_frames"); got != "expr:gte(t,n_forced*2)" {
		t.Errorf("force_key_frames = %q, want the 2s GOP expression", got)
	}
	if vf := argValue(args, "-vf"); !strings.Contains(vf, "scale=-2:'min(1080,ih)'") {
		t.Errorf("-vf = %q, want software scale", vf)
	}
	if hasFlag(args, "-init_hw_device") {
		t.Error("libx264 must not initialise a hardware device")
	}
}

func TestEncodeArgsVAAPI(t *testing.T) {
	args := EncodeArgs(EncodeOptions{
		PipeFormat:          "mpegts",
		OutputFormat:        "mpegts",
		VideoEncoder:        &h264VAAPI,
		VideoBitrate:        "4M",
		VideoMaxrate:        "4M",
		VideoBufsize:        "8M",
		VideoMaxHeight:      1080,
		KeyframeIntervalSec: 2,
		AudioCodec:          "aac",
	})

	if got := argValue(args, "-init_hw_device"); got != "vaapi=va:"+vaapiRenderNode {
		t.Errorf("hw device = %q, want vaapi=va:%s", got, vaapiRenderNode)
	}
	if got := argValue(args, "-filter_hw_device"); got != "va" {
		t.Errorf("filter hw device = %q, want va", got)
	}
	if got := argValue(args, "-c:v"); got != "h264_vaapi" {
		t.Fatalf("video codec = %q, want h264_vaapi", got)
	}
	if got := argValue(args, "-b:v"); got != "4M" {
		t.Errorf("bitrate = %q, want 4M", got)
	}
	if got := argValue(args, "-maxrate"); got != "4M" {
		t.Errorf("maxrate = %q, want 4M (VBV cap applies to VA-API too)", got)
	}
	if got := argValue(args, "-bufsize"); got != "8M" {
		t.Errorf("bufsize = %q, want 8M", got)
	}
	if got := argValue(args, "-force_key_frames"); got != "expr:gte(t,n_forced*2)" {
		t.Errorf("force_key_frames = %q, want the 2s GOP expression", got)
	}
	vf := argValue(args, "-vf")
	if !strings.HasSuffix(vf, "format=nv12,hwupload") {
		t.Errorf("-vf = %q, want to end with the GPU upload (format=nv12,hwupload)", vf)
	}
	if !strings.Contains(vf, "scale=-2:'min(1080,ih)'") {
		t.Errorf("-vf = %q, want CPU scale before hwupload", vf)
	}
	// libx264 presets are invalid for the VA-API encoder; none must be emitted.
	if hasFlag(args, "-preset") {
		t.Error("h264_vaapi must not set -preset")
	}
	// VA-API downconverts via the format=nv12 filter; -pix_fmt would target the
	// GPU surface and must not appear. Nor VideoToolbox's -g workaround.
	if hasFlag(args, "-pix_fmt") {
		t.Error("h264_vaapi must not set -pix_fmt (handled by format=nv12)")
	}
	if hasFlag(args, "-g") {
		t.Error("h264_vaapi must not set -g")
	}
}

func TestEncodeArgsVideoToolbox(t *testing.T) {
	args := EncodeArgs(EncodeOptions{
		PipeFormat:          "mpegts",
		OutputFormat:        "mpegts",
		VideoEncoder:        &h264VideoToolbox,
		VideoBitrate:        "4M",
		VideoMaxrate:        "4M",
		VideoBufsize:        "8M",
		VideoMaxHeight:      1080,
		KeyframeIntervalSec: 2,
		AudioCodec:          "aac",
	})

	if got := argValue(args, "-c:v"); got != "h264_videotoolbox" {
		t.Fatalf("video codec = %q, want h264_videotoolbox", got)
	}
	if got := argValue(args, "-b:v"); got != "4M" {
		t.Errorf("bitrate = %q, want 4M", got)
	}
	if got := argValue(args, "-maxrate"); got != "4M" {
		t.Errorf("maxrate = %q, want 4M (VBV cap)", got)
	}
	if got := argValue(args, "-bufsize"); got != "8M" {
		t.Errorf("bufsize = %q, want 8M", got)
	}
	if got := argValue(args, "-pix_fmt"); got != "yuv420p" {
		t.Errorf("pix_fmt = %q, want yuv420p", got)
	}
	// VideoToolbox's default GOP is sub-second; -g 600 lifts it so
	// force_key_frames sets the real cadence.
	if got := argValue(args, "-g"); got != "600" {
		t.Errorf("-g = %q, want 600 (lift VideoToolbox's default GOP)", got)
	}
	if got := argValue(args, "-force_key_frames"); got != "expr:gte(t,n_forced*2)" {
		t.Errorf("force_key_frames = %q, want the 2s GOP expression", got)
	}
	if hasFlag(args, "-init_hw_device") {
		t.Error("videotoolbox takes system-memory frames, no hardware device init")
	}
	if vf := argValue(args, "-vf"); strings.Contains(vf, "hwupload") {
		t.Errorf("videotoolbox must not hwupload; -vf = %q", vf)
	}
}

func TestEncodeArgsSubtitlesBurnIn(t *testing.T) {
	// The planner supplies a real encoder whenever subtitles are burned in
	// (drawtext operates on decoded frames), so drawtext joins the filter chain
	// and the encoder runs.
	args := EncodeArgs(EncodeOptions{
		PipeFormat:       "mpegts",
		OutputFormat:     "mpegts",
		VideoEncoder:     &libx264,
		SubtitleTextFile: "/tmp/cue.txt",
		AudioCodec:       "aac",
	})

	if got := argValue(args, "-c:v"); got != "libx264" {
		t.Fatalf("video codec = %q, want libx264", got)
	}
	if vf := argValue(args, "-vf"); !strings.Contains(vf, "drawtext") {
		t.Errorf("-vf = %q, want drawtext burn-in", vf)
	}
}

func TestSelectEncoderFallsBackToSoftware(t *testing.T) {
	// A bogus ffmpeg path makes every hardware test-encode fail, so selection
	// returns the always-available software baseline for the codec. Each codec
	// resolves to its own baseline, which is the whole point of the registry.
	for _, tc := range []struct {
		codec media.Codec
		want  string
	}{
		{media.CodecH264, "libx264"},
		{media.CodecHEVC, "libx265"},
	} {
		enc, ok := SelectEncoder(context.Background(), "/nonexistent-ffmpeg-binary", tc.codec)
		if !ok || enc.Name != tc.want {
			t.Errorf("SelectEncoder(%s) = %q (ok=%v), want %q", tc.codec, enc.Name, ok, tc.want)
		}
	}
}

func TestSelectEncoderUnknownCodec(t *testing.T) {
	// A codec with no registered encoder reports ok=false rather than guessing.
	if _, ok := SelectEncoder(context.Background(), "/nonexistent-ffmpeg-binary", media.Codec("av1")); ok {
		t.Error("SelectEncoder(av1) reported ok, but no AV1 encoder is registered")
	}
}
