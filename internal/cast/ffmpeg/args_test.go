package ffmpeg

import (
	"context"
	"slices"
	"strings"
	"testing"
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
	if got := argValue(args, "-c:a"); got != "aac" {
		t.Errorf("audio codec = %q, want aac", got)
	}
}

func TestEncodeArgsLibx264(t *testing.T) {
	args := EncodeArgs(EncodeOptions{
		PipeFormat:     "mpegts",
		OutputFormat:   "mpegts",
		VideoEncoder:   &libx264,
		VideoBitrate:   "4M",
		VideoMaxHeight: 1080,
		AudioCodec:     "aac",
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
	if vf := argValue(args, "-vf"); !strings.Contains(vf, "scale=-2:'min(1080,ih)'") {
		t.Errorf("-vf = %q, want software scale", vf)
	}
	if hasFlag(args, "-init_hw_device") {
		t.Error("libx264 must not initialise a hardware device")
	}
}

func TestEncodeArgsVAAPI(t *testing.T) {
	args := EncodeArgs(EncodeOptions{
		PipeFormat:     "mpegts",
		OutputFormat:   "mpegts",
		VideoEncoder:   &h264VAAPI,
		VideoBitrate:   "4M",
		VideoMaxHeight: 1080,
		AudioCodec:     "aac",
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
}

func TestEncodeArgsVideoToolbox(t *testing.T) {
	args := EncodeArgs(EncodeOptions{
		PipeFormat:     "mpegts",
		OutputFormat:   "mpegts",
		VideoEncoder:   &h264VideoToolbox,
		VideoBitrate:   "4M",
		VideoMaxHeight: 1080,
		AudioCodec:     "aac",
	})

	if got := argValue(args, "-c:v"); got != "h264_videotoolbox" {
		t.Fatalf("video codec = %q, want h264_videotoolbox", got)
	}
	if got := argValue(args, "-b:v"); got != "4M" {
		t.Errorf("bitrate = %q, want 4M", got)
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
		SubtitleFontFile: "/font.ttf",
		AudioCodec:       "aac",
	})

	if got := argValue(args, "-c:v"); got != "libx264" {
		t.Fatalf("video codec = %q, want libx264", got)
	}
	if vf := argValue(args, "-vf"); !strings.Contains(vf, "drawtext") {
		t.Errorf("-vf = %q, want drawtext burn-in", vf)
	}
}

func TestSelectH264EncoderFallsBackToSoftware(t *testing.T) {
	// A bogus ffmpeg path makes every hardware test-encode fail, so selection
	// must return the always-available software baseline.
	enc := SelectH264Encoder(context.Background(), "/nonexistent-ffmpeg-binary")
	if enc.Name != "libx264" {
		t.Fatalf("encoder = %q, want libx264 fallback", enc.Name)
	}
}
