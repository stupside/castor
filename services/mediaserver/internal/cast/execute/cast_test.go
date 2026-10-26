package execute

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	hlsoutput "github.com/stupside/castor/services/mediaserver/internal/cast/output/hls"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/progressive"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/probe"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/dash"
	"github.com/stupside/castor/services/mediaserver/internal/source/hls"
	"github.com/stupside/castor/services/mediaserver/internal/testhttp"
)

// fakeDevice plays what it is handed; if drain is set, it fetches the served stream and keeps it in served.
type fakeDevice struct {
	caps   media.Capabilities
	drain  bool
	refuse error
	served []byte
	ended  bool
}

// finished is a process that has run and exited cleanly, standing in for a read's ffmpeg.
func finished(t *testing.T) *ffmpeg.Process {
	t.Helper()
	proc, err := ffmpeg.Start(t.Context(), "true", ffmpeg.Command{}, ffmpeg.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Wait(); err != nil {
		t.Fatal(err)
	}
	return proc
}

// loopback serves deliveries where these tests' devices reach them.
type loopback struct{}

func (loopback) Publish(ctx context.Context, h http.Handler) (output.Publication, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return testhttp.Publish(ctx, ln, h), nil
}

var _ Device = (*fakeDevice)(nil)

func (d *fakeDevice) Play(ctx context.Context, streamURL *url.URL, _ string, _ []media.Caption) error {
	if d.refuse != nil {
		return d.refuse
	}
	if !d.drain {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL.String(), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	d.served, err = io.ReadAll(resp.Body)
	return err
}

// AwaitEnd ends at once for a device that drained the stream in Play: it played what it took.
func (d *fakeDevice) AwaitEnd(ctx context.Context) error {
	if d.ended || d.drain {
		return ctx.Err()
	}
	<-ctx.Done()
	return context.Cause(ctx)
}

func (d *fakeDevice) Capabilities() media.Capabilities { return d.caps }

func chromecastLike(accepts ...string) media.Capabilities {
	caps := media.Capabilities{SelfFetch: true}
	for _, contentType := range accepts {
		transport := media.TransportMP4
		switch contentType {
		case media.HLS:
			transport = media.TransportHLS
		case media.MPEGTS:
			transport = media.TransportMPEGTS
		case media.MKV:
			transport = media.TransportMKV
		case media.WebM:
			transport = media.TransportWebM
		}
		caps.Formats = append(caps.Formats, media.FormatSupport{Transport: transport, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}})
	}
	return caps
}

func dlnaLike() media.Capabilities {
	return media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMPEGTS, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}}
}

// TestEncoderFailureFailsTheCast is the regression test for "castor exited 0 having cast nothing".
func TestEncoderFailureFailsTheCast(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	srv := httptest.NewServer(http.HandlerFunc(http.NotFound))
	t.Cleanup(srv.Close)
	sourceURL, err := url.Parse(srv.URL + "/gone.mp4")
	if err != nil {
		t.Fatal(err)
	}

	// Rejects the source container, so it is remuxed by an encoder that cannot open its input.
	dev := &fakeDevice{
		caps:  media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMPEGTS, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC}}}, SelfFetch: true},
		drain: true,
	}
	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	err = castOnce(ctx, t, realCast(dev, ffmpegPath, ffprobePath), &source.Stream{URL: sourceURL, ContentType: media.MP4})
	if err == nil {
		t.Fatal("the cast reported success though its encoder died before producing anything")
	}
}

func TestADeadReadIsReportedAsTheReadsOwnFailure(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	srv := httptest.NewServer(http.HandlerFunc(http.NotFound))
	t.Cleanup(srv.Close)
	sourceURL, err := url.Parse(srv.URL + "/gone.mp4")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	program := programFromStream(t, &source.Stream{URL: sourceURL, ContentType: media.MP4})
	out := realCast(&fakeDevice{caps: dlnaLike()}, ffmpegPath, ffprobePath).
		Run(ctx, recovery.Attempt{Try: 1, Program: program, Fetch: sourceFetchPlan(t, program, testReadDeadline)})

	if out.Evidence.ReadErr == nil || !errors.Is(out.Err, out.Evidence.ReadErr) {
		t.Fatalf("the cast reports %v with read error %v, want the read's own failure", out.Err, out.Evidence.ReadErr)
	}
	if out.Evidence.Reached != health.Reading {
		t.Errorf("reached %s, want %s", out.Evidence.Reached, health.Reading)
	}
	if out.Evidence.ReadExit <= 0 {
		t.Errorf("exit status %d for a reader that exited on its own account", out.Evidence.ReadExit)
	}
	if got := out.Evidence.Copied; !got.Video || !got.Audio {
		t.Errorf("the outcome says the reader was copying %s, want both halves", got)
	}
}

func TestAnAudioOnlyCastDoesNotRequireAVideoEncoder(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveGenerated(t, ffmpegPath, "audio.mp4", "/audio.mp4", media.MP4,
		"-map", "1:a", "-c:a", "aac", "-ac", "2")
	caps := dlnaLike()
	caps.Formats[0].Video = media.VideoSupport{}
	dev := &fakeDevice{caps: caps, drain: true}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := castOnce(ctx, t, realCast(dev, ffmpegPath, ffprobePath), origin.stream()); err != nil {
		t.Fatalf("playing supported audio with no video track: %v", err)
	}
	if len(dev.served) == 0 {
		t.Fatal("the audio-only cast delivered no bytes")
	}
}

// testReadDeadline is the mid-read stall bound every cast in this suite reads with.
const testReadDeadline = 30 * time.Second

func castOnce(ctx context.Context, t *testing.T, c Cast, candidate *source.Stream) error {
	t.Helper()
	program := programFromStream(t, candidate)
	return c.Run(ctx, recovery.Attempt{
		Try:     1,
		Program: program,
		Fetch:   sourceFetchPlan(t, program, testReadDeadline),
	}).Err
}

func sourceFetchPlan(t *testing.T, program media.Program, rwTimeout time.Duration) fetch.Plan {
	t.Helper()
	return uniformFetchPlan(program, fetch.For(media.Fetch{}, rwTimeout))
}

func uniformFetchPlan(program media.Program, policy fetch.Policy) fetch.Plan {
	plan := make(fetch.Plan, len(program.Inputs))
	for _, input := range program.Inputs {
		plan[input.ID] = policy
	}
	return plan
}

const castTimeout = 90 * time.Second

// formats are the source formats the engine opens inputs with, as the media server binds them.
var formats = source.Formats{hls.Format{}, dash.Format{}}

// realCast is a cast on dev over the real ffmpeg tools, served where these tests' devices reach it.
func realCast(dev Device, ffmpegPath, ffprobePath string) Cast {
	return Cast{
		Outputs:    testOutputs(),
		FFmpegPath: ffmpegPath,
		Binary:     ffmpeg.Inspect(ffmpegPath),
		Encoders:   ffmpeg.Encoders(ffmpegPath),
		Probes:     probe.FFprobe(ffprobePath),
		Timelines:  direct{},
		InputArgs:  formats.InputArgs,
		Device:     dev,
		Publisher:  loopback{},
		MaxHeight:  1080,
	}
}

// direct follows no timeline: every input is read as the origin publishes it.
type direct struct{}

func (direct) Republish(_ context.Context, program media.Program) (media.Program, func() error, error) {
	return program, func() error { return nil }, nil
}

type fixtureOrigin struct {
	server      *httptest.Server
	path        string
	contentType string
}

func (o fixtureOrigin) stream() *source.Stream {
	u, _ := url.Parse(o.server.URL + o.path)
	return &source.Stream{URL: u, ContentType: o.contentType}
}

// serveFixture serves a one-second H.264/AAC mp4.
func serveFixture(t *testing.T, ffmpegPath string) fixtureOrigin {
	t.Helper()
	return serveGenerated(t, ffmpegPath, "fixture.mp4", "/movie.mp4", media.MP4,
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-profile:v", "baseline",
		"-c:a", "aac", "-ac", "2", "-shortest", "-movflags", "+faststart",
	)
}

func serveSilentFixture(t *testing.T, ffmpegPath string) fixtureOrigin {
	t.Helper()
	return serveGenerated(t, ffmpegPath, "silent.mp4", "/silent.mp4", media.MP4,
		"-map", "0:v", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-profile:v", "baseline", "-movflags", "+faststart",
	)
}

func serveGenerated(t *testing.T, ffmpegPath, filename, urlPath, contentType string, outputArgs ...string) fixtureOrigin {
	t.Helper()
	path := generateFixture(t, ffmpegPath, filename, 1, outputArgs)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, path)
	}))
	t.Cleanup(server.Close)
	return fixtureOrigin{server: server, path: urlPath, contentType: contentType}
}

// generateFixture renders one synthetic program to disk and returns its path.
func generateFixture(t *testing.T, ffmpegPath, filename string, seconds int, outputArgs []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), filename)
	duration := strconv.Itoa(seconds)
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=15:duration=" + duration,
		"-f", "lavfi", "-i", "sine=frequency=440:duration=" + duration,
	}
	args = append(args, outputArgs...)
	args = append(args, path)

	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, ffmpegPath, args...).CombinedOutput(); err != nil {
		t.Fatalf("generating %s: %v\n%s", filename, err, out)
	}
	return path
}

func requireFFmpegTools(t *testing.T) (ffmpeg, ffprobe string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	ffprobe, err = exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH")
	}
	return ffmpeg, ffprobe
}

func programFromStream(t *testing.T, stream *source.Stream) media.Program {
	t.Helper()
	program, err := source.ProgramFor(stream)
	if err != nil {
		t.Fatal(err)
	}
	if stream.Observation == nil && stream.Probe != nil {
		program.SetMeasurement(*stream.Probe)
	}
	return program
}

func (*fakeDevice) Pause(context.Context) error  { return nil }
func (*fakeDevice) Resume(context.Context) error { return nil }

func testOutputs() output.Factory { return output.Factory{progressive.Adapter{}, hlsoutput.Adapter{}} }
