package encoding

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestActualEncodeHonorsBaselineLevelAndDimensions(t *testing.T) {
	caps := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264, Profiles: []media.Profile{media.ProfileBaseline}, MaxLevel: 31}, Audio: media.AudioSupport{Codec: media.CodecAAC}}}}
	format, _ := container.For(media.MP4)
	plan, err := codec.Served(t.Context(), codec.Inputs{Caps: caps, Into: format, Measured: true, MaxHeight: 2160, Probe: media.ProbeInfo{VideoCodec: media.CodecH264, VideoProfile: media.ProfileHigh, VideoHeight: 2160, VideoBitDepth: 8}, Encoders: func(context.Context, media.Codec, bool) (ffmpeg.Encoder, bool) {
		return ffmpeg.Encoder{Name: "libx264", Codec: media.CodecH264, Flags: []string{"-preset", "veryfast", "-pix_fmt", "yuv420p"}}, true
	}})
	if err != nil {
		t.Fatal(err)
	}
	enc, ok := plan.Video.Encode()
	if !ok {
		t.Fatal("expected reencode")
	}
	output := filepath.Join(t.TempDir(), "bounded.mp4")
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=2048x256:rate=60:duration=0.2", "-vf", strings.Join(videoFilters(enc), ","), "-c:v", enc.Encoder.Name}
	args = append(args, videoEncodeArgs(enc)...)
	args = append(args, output)
	if out, err := exec.CommandContext(t.Context(), "ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("encode: %v\n%s", err, out)
	}
	out, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error", "-show_streams", "-of", "json", output).Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Streams []struct {
			Codec       string `json:"codec_name"`
			Profile     string `json:"profile"`
			Level       int    `json:"level"`
			Width       int    `json:"width"`
			Height      int    `json:"height"`
			PixelFormat string `json:"pix_fmt"`
			Rate        string `json:"avg_frame_rate"`
		}
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Streams) != 1 {
		t.Fatalf("streams=%s", out)
	}
	s := result.Streams[0]
	if s.Width > 1280 || s.Height > 720 || s.Rate != "30/1" || s.PixelFormat != "yuv420p" {
		t.Fatalf("output exceeds envelope: %s", out)
	}
	if !caps.CanCopyVideo(media.ProbeInfo{VideoCodec: media.Codec(s.Codec), VideoProfile: media.Profile(s.Profile), VideoLevel: s.Level, VideoBitDepth: 8}) {
		t.Fatalf("output still rejected by receiver: %s", out)
	}
}
