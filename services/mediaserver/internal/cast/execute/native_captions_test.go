package execute

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/hls"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

func TestGeneratedNativeCaptionsCopyVideoAndServeAnHLSRendition(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	body, err := os.ReadFile(generateFixture(t, ffmpegPath, "native.ts", 8,
		[]string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-g", "50", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"}))
	if err != nil {
		t.Fatal(err)
	}
	s, buf := bufferedCast(t, ffmpegPath, ffprobePath)
	s.FFmpegPath = ffmpegPath
	s.Publisher = loopback{}
	s.capabilities = chromecastLike(media.HLS, media.MP4)
	s.capabilities.Formats[0].Transport = media.TransportHLSWebVTT
	offset := time.Duration(0)
	buf.captionVideoOffset = &offset
	if _, err := buf.reader.spool.Write(body); err != nil {
		t.Fatal(err)
	}
	buf.reader.spool.CloseWrite(nil)
	t.Cleanup(func() { _ = buf.reader.spool.Close() })
	transcript := &fakeTranscript{}
	transcript.cues.Commit([]subtitle.Word{{Start: 1, End: 2, Text: "Generated caption."}}, 8)
	transcript.cues.Close()
	buf.transcript = transcript
	f := feed{buffered: buf}
	opts, err := s.encode(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Format.ContentType != media.HLS || opts.Video.Name() != "copy" {
		t.Fatalf("native captions selected %s / %s", opts.Format.ContentType, opts.Video.Name())
	}
	if !opts.captioned {
		t.Fatal("a delivery that lists the generated captions does not say so")
	}
	cmd, err := encoding.EncodeArgs(opts.EncodeOptions)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "drawtext") {
		t.Fatal("native captions burn text into video")
	}
	d, err := s.produce(t.TempDir(), f, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := awaitArtifact(t.Context(), d); err != nil {
		t.Fatalf("%v: %v", err, d.output.Evidence())
	}
	master := getCaptionArtifact(t, t.Context(), d.URL())
	if !strings.Contains(master, "TYPE=SUBTITLES") || !strings.HasSuffix(d.URL().Path, "/master.m3u8") {
		t.Fatalf("not handed a subtitle-enabled master: %s", master)
	}
	captions := getCaptionArtifact(t, t.Context(), d.URL().ResolveReference(&url.URL{Path: "captions.m3u8"}))
	var text strings.Builder
	for line := range strings.SplitSeq(captions, "\n") {
		if strings.HasSuffix(line, ".vtt") {
			text.WriteString(getCaptionArtifact(t, t.Context(), d.URL().ResolveReference(&url.URL{Path: line})))
		}
	}
	if !strings.Contains(text.String(), "Generated caption.") || !strings.Contains(text.String(), "X-TIMESTAMP-MAP") {
		t.Fatalf("generated cues did not reach HTTP: %s", text.String())
	}
}

func TestStaticVTTSupportDoesNotSelectProgressiveSubtitleDelivery(t *testing.T) {
	caps := chromecastLike(media.HLS, media.MP4)
	caps.Formats[0].Captions = []media.CaptionFormat{media.CaptionVTT}
	s := testPreparation(t.Context(), Cast{Outputs: testOutputs(), Device: &fakeDevice{caps: caps}}, recovery.Attempt{})
	t.Cleanup(func() { _ = s.releases.release() })
	offset := time.Duration(0)
	f := feed{buffered: &buffered{transcript: &fakeTranscript{}, captionVideoOffset: &offset}}
	if _, ok := s.nativeOutput(f); ok {
		t.Fatal("static VTT support incorrectly enabled streaming captions")
	}
	caps.Formats = []media.FormatSupport{{Transport: media.TransportMP4, Captions: []media.CaptionFormat{media.CaptionVTT}}}
	s.capabilities = caps
	if _, ok := s.nativeOutput(f); ok {
		t.Fatal("native captions selected for MP4 with static VTT captions")
	}
}

func getCaptionArtifact(t *testing.T, ctx context.Context, u *url.URL) string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("%s: %d %v %s", u, res.StatusCode, err, body)
	}
	return string(body)
}

func TestNativeCaptionsRequireVideoInsideFirstFragment(t *testing.T) {
	caps := chromecastLike(media.HLS, media.MP4)
	caps.Formats[0].Transport = media.TransportHLSWebVTT
	s := testPreparation(t.Context(), Cast{Outputs: testOutputs(), Device: &fakeDevice{caps: caps}}, recovery.Attempt{})
	t.Cleanup(func() { _ = s.releases.release() })
	for _, tt := range []struct {
		name   string
		offset *time.Duration
		native bool
	}{
		{"unknown", nil, false},
		{"known zero", new(time.Duration), true},
		{"inside first fragment", new(hls.CaptionSegmentDuration - time.Nanosecond), true},
		{"first fragment boundary", new(hls.CaptionSegmentDuration), false},
		{"late video", new(6 * time.Second), false},
		{"negative clock", new(-time.Second), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := feed{buffered: &buffered{transcript: &fakeTranscript{}, captionVideoOffset: tt.offset}}
			if _, got := s.nativeOutput(f); got != tt.native {
				t.Fatalf("native=%v want%v", got, tt.native)
			}
		})
	}
}

func TestSixSecondVideoDelaySelectsBurnBeforeEncoding(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	path := filepath.Join(t.TempDir(), "late-video.mp4")
	cmd := exec.CommandContext(t.Context(), ffmpegPath, "-hide_banner", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=25:duration=4", "-f", "lavfi", "-i", "sine=frequency=440:duration=10", "-vf", "setpts=PTS+6/TB", "-fps_mode", "passthrough", "-c:v", "libx264", "-c:a", "aac", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	s, buf := bufferedCast(t, ffmpegPath, ffprobePath)
	s.capabilities = chromecastLike(media.HLS, media.MP4)
	s.capabilities.Formats[0].Transport = media.TransportHLSWebVTT
	info, _, err := s.Probes.File(path).Probe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.VideoStartOffset == nil || *info.VideoStartOffset < 5900*time.Millisecond {
		t.Fatalf("fixture has wrong video clock: %+v", info.VideoStartOffset)
	}
	buf.captionVideoOffset = info.VideoStartOffset
	transcript := &fakeTranscript{}
	buf.transcript = transcript
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buf.reader.spool.Write(body); err != nil {
		t.Fatal(err)
	}
	buf.reader.spool.CloseWrite(nil)
	t.Cleanup(func() { buf.reader.spool.Close() })
	f := feed{buffered: buf}
	if _, ok := s.nativeOutput(f); ok {
		t.Fatal("late-video source selected native captions")
	}
	burn := subtitle.NewCueFile(t.TempDir(), transcript.Cues())
	opts, err := s.encode(f, burn)
	if err != nil {
		t.Fatal(err)
	}
	if opts.captioned {
		t.Fatal("a burned-in cast says its playlist lists the captions")
	}
	args, err := encoding.EncodeArgs(opts.EncodeOptions)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args.Args, " "), "drawtext") {
		t.Fatal("late-video source did not retain burn fallback")
	}
}
