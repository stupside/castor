package execute

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

func TestEncodeTriesNextTransportWhenHostCannotEncodeForFirst(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	body, err := os.ReadFile(generateFixture(t, ffmpegPath, "fallback.ts", 1,
		[]string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"}))
	if err != nil {
		t.Fatal(err)
	}
	s, buf := bufferedCast(t, ffmpegPath, ffprobePath)
	t.Cleanup(func() { _ = buf.reader.spool.Close() })
	if _, err := buf.reader.spool.Write(body); err != nil {
		t.Fatal(err)
	}
	buf.reader.spool.CloseWrite(nil)
	s.capabilities = media.Capabilities{Formats: []media.FormatSupport{
		{Transport: media.TransportHLS, Video: media.VideoSupport{Codec: media.CodecHEVC}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}},
		{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}},
	}}
	s.Encoders = func(context.Context, media.Codec, bool) (ffmpeg.Encoder, bool) { return ffmpeg.Encoder{}, false }
	opts, err := s.encode(feed{buffered: buf}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Format.ContentType != media.MP4 || opts.Video.Name() != "copy" || opts.Audio.Name() != "copy" {
		t.Fatalf("fallback envelope = %s, video=%s audio=%s", opts.Format.ContentType, opts.Video.Name(), opts.Audio.Name())
	}
}

// TestTheBurnInsCueReachesTheEncodeThatDrawsIt: the cue must be an input before the copy decision.
func TestTheBurnInsCueReachesTheEncodeThatDrawsIt(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)

	cuePath := filepath.Join(t.TempDir(), "cue.txt")
	if err := os.WriteFile(cuePath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, buf := bufferedCast(t, ffmpegPath, ffprobePath)

	s.capabilities = dlnaLike()
	opts, err := s.encode(feed{buffered: buf}, subtitle.NewCueFile(filepath.Dir(cuePath), &subtitle.Cues{}))
	if err != nil {
		t.Fatalf("building the encode: %v", err)
	}
	cmd, err := encoding.EncodeArgs(opts.EncodeOptions)
	if err != nil {
		t.Fatalf("building the args: %v", err)
	}
	if !slices.ContainsFunc(cmd.Args, func(arg string) bool { return strings.Contains(arg, cuePath) }) {
		t.Errorf("no argument names the cue file the burn-in prepared: %v", cmd.Args)
	}
}

func TestTheBufferedEncodeIsMeasuredFromTheBufferAndNotTheSource(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)

	program, err := os.ReadFile(generateFixture(t, ffmpegPath, "buffer.ts", 1,
		[]string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"}))
	if err != nil {
		t.Fatal(err)
	}

	s, buf := bufferedCast(t, ffmpegPath, ffprobePath)
	if _, err := buf.reader.spool.Write(program); err != nil {
		t.Fatal(err)
	}
	// A source nothing can measure: nothing is listening on that port.
	s.attempt.Program = programFromStream(t, &source.Stream{URL: &url.URL{Scheme: "http", Host: "127.0.0.1:1", Path: "/gone.mp4"}, ContentType: media.MP4})

	s.capabilities = dlnaLike()
	opts, err := s.encode(feed{buffered: buf}, nil)
	if err != nil {
		t.Fatalf("building the encode: %v", err)
	}
	if opts.Video.Name() != "copy" || opts.Probe.VideoCodec != media.CodecH264 {
		t.Errorf("video %q from %+v, want a copy decided from the buffer's own h264", opts.Video.Name(), opts.Probe)
	}
}

// bufferedCast is the read-once composition's material for building an encode (empty buffer).
func bufferedCast(t *testing.T, ffmpegPath, ffprobePath string) (*preparation, *buffered) {
	t.Helper()
	sp, err := storage.NewSpool(filepath.Join(t.TempDir(), "spool.ts"))
	if err != nil {
		t.Fatal(err)
	}
	s := readingAttempt(t, realCast(&fakeDevice{}, ffmpegPath, ffprobePath), recovery.Attempt{})
	return s, &buffered{reader: &pull{spool: sp, done: make(chan struct{})}}
}
