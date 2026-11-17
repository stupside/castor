package execute

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestAReadItsHardwareEncoderFailedIsTheEncodesFailure(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveFixture(t, ffmpegPath)

	c := realCast(&fakeDevice{}, ffmpegPath, ffprobePath)
	working := c.Encoders
	c.Encoders = func(ctx context.Context, codec media.Codec, software bool) (ffmpeg.Encoder, bool) {
		if software || codec != media.CodecH264 {
			return working(ctx, codec, true)
		}
		// libx264 refusing 4:4:4 in this profile as it opens, as a phone's encoder refuses a picture it cannot make.
		return ffmpeg.Encoder{Name: "libx264", Codec: media.CodecH264, Hardware: true, Flags: []string{"-profile:v", "baseline", "-pix_fmt", "yuv444p"}}, true
	}
	program := programFromStream(t, origin.stream())

	for _, software := range []bool{false, true} {
		s := readingAttempt(t, c, recovery.Attempt{Program: program, Fetch: sourceFetchPlan(t, program, testReadDeadline), Decode: media.Axes{Video: true}, Software: software})
		followed, unfollow, err := s.follow(program)
		if err != nil {
			t.Fatal(err)
		}
		s.releases.push(unfollow)
		buf, err := s.read(t.TempDir(), followed)
		if err != nil {
			t.Fatal(err)
		}
		<-buf.reader.Done()
		e := evidence(ran{reader: buf.reader}, buf.reader.Err(), false)
		switch {
		case !software && (e.EncodeErr == nil || !e.Hardware):
			t.Errorf("the hardware read ended on %v with encode error %v (hardware %t), want its encoder blamed\n%q", e.ReadErr, e.EncodeErr, e.Hardware, e.Lines)
		case software && (e.ReadErr != nil || e.EncodeErr != nil):
			t.Errorf("the software read ended on %v (encode error %v), want it whole", e.ReadErr, e.EncodeErr)
		}
	}
}

// A remux reads the source in the encoder's own ffmpeg: its exit is the encoder's only where a hardware encoder's errors explain it.
func TestARemuxExitIsTheHardwareEncodersWhereItsOwnErrorsSaySo(t *testing.T) {
	for _, tt := range []struct {
		name     string
		line     string
		hardware bool
		own      bool
	}{
		{"a hardware encoder's own error", "[enc:h264_mediacodec @ 0x2] [error] Error encoding a frame", true, true},
		{"the source's error under a hardware encode", "[in#0/hls @ 0x1] [error] Error during demuxing", true, false},
		{"a software encoder's own error", "[enc:libx264 @ 0x2] [error] Error encoding a frame", false, false},
	} {
		script := fmt.Sprintf("echo '%s' >&2; exit 3", tt.line)
		proc, err := ffmpeg.Start(t.Context(), "/bin/sh", ffmpeg.Command{Args: []string{"-c", script}}, ffmpeg.Options{})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, proc.Stdout)
		if _, own := errors.AsType[*encoderFailed](encoderResult(t.Context(), proc, tt.hardware, false)); own != tt.own {
			t.Errorf("%s: the encode's own failure %t, want %t", tt.name, own, tt.own)
		}
	}
}

func TestOnlyTheEncodersOwnErrorsBlameIt(t *testing.T) {
	for _, tt := range []struct {
		line string
		want bool
	}{
		{"[vost#0:0/h264_mediacodec @ 0x1] [enc:h264_mediacodec @ 0x2] [error] Error encoding a frame: Generic error in an external library", true},
		{"[h264_mediacodec @ 0x1] [error] Failed to configure the encoder", true},
		{"[in#0/hls @ 0x1] [error] Error during demuxing: Input/output error", false},
		{"[vost#0:0/h264_mediacodec @ 0x1] [enc:h264_mediacodec @ 0x2] [warning] Frame rate unknown", false},
		{"[aost#0:1/aac @ 0x1] [enc:aac @ 0x2] [error] Error encoding a frame", false},
	} {
		if got := failedItself("h264_mediacodec", []string{tt.line}); got != tt.want {
			t.Errorf("%q blames the encoder %t, want %t", tt.line, got, tt.want)
		}
	}
}
