package pipeline

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stupside/castor/internal/cast/core"
	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/media"
	"github.com/stupside/castor/internal/source/resolve"
)

// fakeDevice is a Device stand-in that records what Run tells it to Play. On a
// served cast it also fetches the URL it is handed: the replay-served path blocks
// until a client reads the stream to EOF, so without a real reader Wait would hang
// the whole idle-grace window. Pass-through leaves drain false: the source URL is
// not one of our servers and must never be fetched.
type fakeDevice struct {
	caps  media.Renderer
	drain bool

	mu    sync.Mutex
	plays []playCall
}

type playCall struct {
	url         string
	contentType string
}

// Compile-time proof the fake satisfies the interface Run consumes.
var _ device.Device = (*fakeDevice)(nil)

func (d *fakeDevice) Play(ctx context.Context, streamURL *url.URL, contentType string) error {
	d.mu.Lock()
	d.plays = append(d.plays, playCall{url: streamURL.String(), contentType: contentType})
	d.mu.Unlock()

	if !d.drain {
		return nil
	}
	// Drain the served stream to EOF so the replay server's Wait can complete.
	// Bounded by ctx (the test sets a timeout), so a wedged producer fails the
	// test rather than hanging it.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL.String(), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

func (d *fakeDevice) Capabilities() media.Renderer           { return d.caps }
func (d *fakeDevice) StreamHeaders(string) map[string]string { return nil }
func (d *fakeDevice) Close() error                           { return nil }

func (d *fakeDevice) snapshot() []playCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.plays)
}

// connectTo is the injected ConnectFunc that hands Run a pre-built fake instead of
// discovering a real renderer, so the executor runs without a live network.
func connectTo(dev device.Device) ConnectFunc {
	return func(context.Context, core.Config) (device.Device, error) { return dev, nil }
}

// TestRunPassthrough is the pure branch: a self-fetching renderer that already
// accepts the source container is handed the source URL directly. No ffmpeg, no
// served stream, so this always runs.
func TestRunPassthrough(t *testing.T) {
	srcURL, err := url.Parse("http://cdn.example.com/movie.mp4")
	if err != nil {
		t.Fatal(err)
	}
	source := &media.Stream{URL: srcURL, ContentType: media.MP4}
	dev := &fakeDevice{caps: media.Renderer{SelfFetch: true, Containers: []string{media.MP4}}}
	// A self-fetching renderer that accepts the source container: NewPlan (inside
	// Run) resolves pass-through, so the derived plan is not passed in.
	cfg := core.Config{Device: core.DeviceConfig{Type: device.TypeChromecast}}

	if err := Run(context.Background(), cfg, connectTo(dev), source, "127.0.0.1"); err != nil {
		t.Fatalf("Run pass-through: %v", err)
	}

	plays := dev.snapshot()
	if len(plays) != 1 {
		t.Fatalf("expected exactly one Play call, got %d: %+v", len(plays), plays)
	}
	if plays[0].url != srcURL.String() {
		t.Errorf("Play url = %q, want the source URL %q (the device fetches it itself)", plays[0].url, srcURL.String())
	}
	if plays[0].contentType != media.MP4 {
		t.Errorf("Play content type = %q, want %q (the source's own type)", plays[0].contentType, media.MP4)
	}
}

// TestRunServeRemux is the network-remux branch: a self-fetching renderer that
// rejects the source container. Run must remux to the plan's mp4 output and serve
// it, pointing the device at the local replay server rather than the source URL.
func TestRunServeRemux(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)

	origin := serveFixture(t, ffmpegPath)
	source := origin.stream()

	// Self-fetching, but only accepts MKV, so the mp4 source reaches the remux.
	// Advertise AAC stereo so the source audio is copied, not re-encoded, and mp4
	// as the served container (a self-fetching renderer must declare one).
	dev := &fakeDevice{
		caps: media.Renderer{
			SelfFetch:       true,
			Containers:      []string{media.MKV},
			ServedContainer: media.MP4,
			Audio:           []media.AudioSupport{{Codec: media.CodecAAC, MaxChannels: 2}},
		},
		drain: true,
	}
	runServed(t, dev, device.TypeChromecast, source, ffmpegPath, ffprobePath)
	assertServed(t, dev, media.MP4, source.URL.String())
}

// TestRunServeSpool is the read-once spool branch: a renderer that never
// self-fetches (DLNA). Run must pull the single-use source into a local spool and
// serve the MPEG-TS remux the plan names. Advertising the fixture's own codecs
// makes the copy gate stream-copy, so no encoder probe runs; the assertion holds
// regardless, since the served content type is the plan's output either way.
func TestRunServeSpool(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)

	origin := serveFixture(t, ffmpegPath)
	source := origin.stream()

	dev := &fakeDevice{
		caps: media.Renderer{
			Video: []media.VideoSupport{{Codec: media.CodecH264}},
			Audio: []media.AudioSupport{{Codec: media.CodecAAC, MaxChannels: 2}},
		},
		drain: true,
	}
	runServed(t, dev, device.TypeDLNA, source, ffmpegPath, ffprobePath)
	assertServed(t, dev, mpegtsContentType, source.URL.String())
}

// mpegtsContentType is what the DLNA-style served path tells the device it is
// fetching.
const mpegtsContentType = media.MPEGTS

// TestRunServeHLS is the live-HLS served branch (a self-fetching renderer that
// rejects the source container and serves HLS, i.e. Roku): Run must remux to a
// local HLS directory and point the device at the .m3u8, never the source URL.
// It drives a real ffmpeg remux, then cancels to end the otherwise-live cast
// rather than wait out the server's idle grace.
func TestRunServeHLS(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)

	origin := serveFixture(t, ffmpegPath)
	source := origin.stream()

	dev := &fakeDevice{
		caps: media.Renderer{
			SelfFetch:       true,
			Containers:      []string{media.MKV}, // rejects the mp4 source -> remux
			ServedContainer: media.HLS,
			Audio:           []media.AudioSupport{{Codec: media.CodecAAC, MaxChannels: 2}},
		},
	}
	cfg := core.Config{
		Device:    core.DeviceConfig{Type: device.TypeChromecast}, // any self-fetch type
		Transcode: core.TranscodeConfig{FFmpegPath: ffmpegPath, RWTimeout: 30 * time.Second},
		Resolver:  resolve.Config{FFprobePath: ffprobePath, MaxHeight: 1080},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, connectTo(dev), source, "127.0.0.1") }()

	// Wait for the device to be pointed at the playlist, then end the live cast.
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	deadline := time.After(45 * time.Second)
	for {
		if plays := dev.snapshot(); len(plays) == 1 {
			if plays[0].contentType != media.HLS {
				t.Errorf("served content type = %q, want %q", plays[0].contentType, media.HLS)
			}
			if !strings.HasSuffix(plays[0].url, ".m3u8") {
				t.Errorf("HLS cast must point the device at a .m3u8 playlist, got %q", plays[0].url)
			}
			if plays[0].url == source.URL.String() {
				t.Error("a served cast must not hand the device the source URL")
			}
			break
		}
		select {
		case err := <-done:
			t.Fatalf("Run returned before playback started: %v", err)
		case <-deadline:
			t.Fatal("device was never pointed at the HLS playlist")
		case <-poll.C:
		}
	}

	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
}

// runServed drives Run for a served plan under a bounded context (a wedged
// ffmpeg fails the test instead of hanging the suite) and fails on any error.
func runServed(t *testing.T, dev *fakeDevice, deviceType device.Type, source *media.Stream, ffmpegPath, ffprobePath string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg := core.Config{
		Device:    core.DeviceConfig{Type: deviceType},
		Transcode: core.TranscodeConfig{FFmpegPath: ffmpegPath, RWTimeout: 30 * time.Second},
		Resolver:  resolve.Config{FFprobePath: ffprobePath, MaxHeight: 1080},
	}
	if err := Run(ctx, cfg, connectTo(dev), source, "127.0.0.1"); err != nil {
		t.Fatalf("Run served: %v", err)
	}
}

// assertServed checks the served cast pointed the device at the local replay
// server (not the source URL) with the plan's output content type.
func assertServed(t *testing.T, dev *fakeDevice, wantCT, sourceURL string) {
	t.Helper()
	plays := dev.snapshot()
	if len(plays) != 1 {
		t.Fatalf("expected exactly one Play call, got %d: %+v", len(plays), plays)
	}
	if plays[0].contentType != wantCT {
		t.Errorf("served content type = %q, want %q", plays[0].contentType, wantCT)
	}
	if plays[0].url == sourceURL {
		t.Errorf("a served cast must point the device at the local replay server, not the source URL %q", sourceURL)
	}
}

// fixtureOrigin is a local HTTP origin serving a generated media file, standing
// in for the upstream a real cast pulls from.
type fixtureOrigin struct{ server *httptest.Server }

// stream is the media.Stream a cast resolves to for this origin. The ".mp4" path
// only shapes the URL; the handler serves the fixture regardless.
func (o fixtureOrigin) stream() *media.Stream {
	u, _ := url.Parse(o.server.URL + "/movie.mp4")
	return &media.Stream{URL: u, ContentType: media.MP4}
}

// serveFixture generates a one-second H.264/AAC mp4 and serves it over local
// HTTP. faststart puts the moov atom up front so ffmpeg can read it streaming,
// and http.ServeFile still honours range requests the demuxer may issue.
func serveFixture(t *testing.T, ffmpegPath string) fixtureOrigin {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.mp4")
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=15:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-profile:v", "baseline",
		"-c:a", "aac", "-ac", "2", "-shortest",
		"-movflags", "+faststart",
		path,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, ffmpegPath, args...).CombinedOutput(); err != nil {
		t.Fatalf("generating fixture: %v\n%s", err, out)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, path)
	}))
	t.Cleanup(server.Close)
	return fixtureOrigin{server: server}
}

// requireFFmpegTools returns the ffmpeg and ffprobe paths, or skips: the served
// branches drive a real remux and probe, so a host without them cannot run them.
func requireFFmpegTools(t *testing.T) (ffmpeg, ffprobe string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH; skipping the served-path engine test (it drives a real remux)")
	}
	ffprobe, err = exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH; skipping the served-path engine test (it probes the stream)")
	}
	return ffmpeg, ffprobe
}
