package pipeline

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stupside/castor/internal/cast/core"
	"github.com/stupside/castor/internal/cast/ffmpeg"
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
	// tee, when set, receives the served bytes as they are drained, so a test can
	// inspect what the renderer was actually handed.
	tee io.Writer

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
	var sink io.Writer = io.Discard
	if d.tee != nil {
		sink = d.tee
	}
	_, err = io.Copy(sink, resp.Body)
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

// TestRunHeaderGatedSourceIsServed is the pass-through guard: the renderer
// self-fetches AND accepts the source container, but the source only ever
// answered to the request headers castor captured. Handing such a URL to the
// renderer strips those headers and the device silently idles, so Run must serve
// the remux instead and never point the device at the source URL.
func TestRunHeaderGatedSourceIsServed(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)

	origin := serveFixture(t, ffmpegPath)
	source := origin.stream()
	source.Headers = http.Header{
		"Referer": {"https://player.example/"},
		"Origin":  {"https://player.example"},
	}

	// Accepts mp4, the source's own container: only the headers force the serve.
	dev := &fakeDevice{
		caps: media.Renderer{
			SelfFetch:       true,
			Containers:      []string{media.MP4},
			ServedContainer: media.MP4,
			Audio:           []media.AudioSupport{{Codec: media.CodecAAC, MaxChannels: 2}},
		},
		drain: true,
	}
	runServed(t, dev, castConfig(device.TypeChromecast, ffmpegPath, ffprobePath), source)
	assertServed(t, dev, media.MP4, source.URL.String())
}

// TestRunServeHeaderGatedHLS is the embed-source shape end to end: HLS that is
// header-gated AND serves its MPEG-TS segments under a .jpg extension. A renderer
// handed that playlist fetches without the headers and rejects the mislabelled
// segments; castor reads it fine, so the cast must come out as a served remux, a
// local mp4 URL and never the source.
func TestRunServeHeaderGatedHLS(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)

	origin := serveHLSFixture(t, ffmpegPath)
	source := origin.stream()
	source.Headers = http.Header{"Referer": {"https://player.example/"}}

	// Capture what the renderer is handed, to check it decodes.
	servedPath := filepath.Join(t.TempDir(), "served.mp4")
	served, err := os.Create(servedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer served.Close()

	dev := &fakeDevice{
		caps: media.Renderer{
			SelfFetch:       true,
			Containers:      []string{media.HLS, media.MP4}, // takes the source container
			ServedContainer: media.MP4,
			Audio:           []media.AudioSupport{{Codec: media.CodecAAC, MaxChannels: 2}},
		},
		drain: true,
		tee:   served,
	}
	runServed(t, dev, castConfig(device.TypeChromecast, ffmpegPath, ffprobePath), source)
	assertServed(t, dev, media.MP4, source.URL.String())

	if err := served.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := ffmpeg.ProbeFile(context.Background(), ffprobePath, servedPath)
	if err != nil {
		t.Fatalf("probing what the renderer was served: %v", err)
	}
	if info.VideoCodec != media.CodecH264 || info.AudioCodec != media.CodecAAC {
		t.Errorf("served stream = %q/%q, want h264/aac: the point of serving is that the renderer receives a well-formed stream",
			info.VideoCodec, info.AudioCodec)
	}
}

// TestRunConfiguredServeIsServed covers the operator's override reaching the
// executor: the source needs no headers and the renderer takes its container, so
// the rule alone would pass it through. Configured serve relays it instead, which
// is the escape hatch for a source castor has no way to convict.
func TestRunConfiguredServeIsServed(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)

	origin := serveFixture(t, ffmpegPath)
	source := origin.stream()

	dev := &fakeDevice{
		caps: media.Renderer{
			SelfFetch:       true,
			Containers:      []string{media.MP4}, // takes the source container
			ServedContainer: media.MP4,
			Audio:           []media.AudioSupport{{Codec: media.CodecAAC, MaxChannels: 2}},
		},
		drain: true,
	}

	cfg := castConfig(device.TypeChromecast, ffmpegPath, ffprobePath)
	cfg.Delivery = core.DeliveryServe

	runServed(t, dev, cfg, source)
	assertServed(t, dev, media.MP4, source.URL.String())
}

// TestRunDemuxedSourceKeepsAudio casts a program whose renditions live at
// separate URLs, the shape an HLS master with an audio group publishes. The
// puller has to read both and mux them back together, so the assertion is on the
// bytes the renderer receives: video AND audio. Reading the video rendition
// alone is the live failure this covers, and it does not fail quietly, ffmpeg
// exits mapping an audio track that is not there.
func TestRunDemuxedSourceKeepsAudio(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)

	origin := serveDemuxedHLSFixture(t, ffmpegPath)
	source := origin.stream()
	if !source.Demuxed() {
		t.Fatal("fixture is not demuxed")
	}

	servedPath := filepath.Join(t.TempDir(), "served.ts")
	served, err := os.Create(servedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer served.Close()

	// A push-only renderer: the whole spool path runs, puller included.
	dev := &fakeDevice{
		caps: media.Renderer{
			Video: []media.VideoSupport{{Codec: media.CodecH264}},
			Audio: []media.AudioSupport{{Codec: media.CodecAAC, MaxChannels: 2}},
		},
		drain: true,
		tee:   served,
	}
	runServed(t, dev, castConfig(device.TypeDLNA, ffmpegPath, ffprobePath), source)
	assertServed(t, dev, mpegtsContentType, source.URL.String())

	if err := served.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := ffmpeg.ProbeFile(context.Background(), ffprobePath, servedPath)
	if err != nil {
		t.Fatalf("probing what the renderer was served: %v", err)
	}
	if info.VideoCodec != media.CodecH264 {
		t.Errorf("served video codec = %q, want h264", info.VideoCodec)
	}
	if info.AudioCodec != media.CodecAAC {
		t.Errorf("served audio codec = %q, want aac: the separate audio rendition never made it into the cast", info.AudioCodec)
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
	runServed(t, dev, castConfig(device.TypeChromecast, ffmpegPath, ffprobePath), source)
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
	runServed(t, dev, castConfig(device.TypeDLNA, ffmpegPath, ffprobePath), source)
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
	cfg := castConfig(device.TypeChromecast, ffmpegPath, ffprobePath) // any self-fetch type

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

// castConfig is the configuration a served-path test runs on: the device family
// (which fixes the connect timing) plus the real ffmpeg tools. A test that needs
// to vary an axis takes this and edits the field it cares about.
func castConfig(deviceType device.Type, ffmpegPath, ffprobePath string) core.Config {
	return core.Config{
		Device:    core.DeviceConfig{Type: deviceType},
		Transcode: core.TranscodeConfig{FFmpegPath: ffmpegPath, RWTimeout: 30 * time.Second},
		Resolver:  resolve.Config{FFprobePath: ffprobePath, MaxHeight: 1080},
	}
}

// runServed drives Run for a served plan under a bounded context (a wedged
// ffmpeg fails the test instead of hanging the suite) and fails on any error.
func runServed(t *testing.T, dev *fakeDevice, cfg core.Config, source *media.Stream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

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

// fixtureOrigin is a local HTTP origin serving generated media, standing in for
// the upstream a real cast pulls from: path is what the cast points at and
// contentType the container it resolved to.
type fixtureOrigin struct {
	server      *httptest.Server
	path        string
	audioPath   string // set when the origin publishes audio as its own rendition
	contentType string
}

// stream is the media.Stream a cast resolves to for this origin. audioPath is
// set only for a demuxed origin, where resolution yields two URLs.
func (o fixtureOrigin) stream() *media.Stream {
	u, _ := url.Parse(o.server.URL + o.path)
	s := &media.Stream{URL: u, ContentType: o.contentType}
	if o.audioPath != "" {
		audio, _ := url.Parse(o.server.URL + o.audioPath)
		s.AudioURL = audio
	}
	return s
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
	return fixtureOrigin{server: server, path: "/movie.mp4", contentType: media.MP4}
}

// serveHLSFixture generates a short HLS stream whose MPEG-TS segments are written
// under a .jpg extension and serves the directory over local HTTP, which labels
// them image/jpeg from that extension. That is the disguise embed CDNs use:
// ffmpeg reads it (castor relaxes the extension checks for every HLS input),
// while a Cast receiver handed the same playlist rejects the segments.
func serveHLSFixture(t *testing.T, ffmpegPath string) fixtureOrigin {
	t.Helper()
	dir := t.TempDir()
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=15:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-profile:v", "baseline",
		"-c:a", "aac", "-ac", "2", "-shortest",
		"-f", "hls",
		"-hls_time", "1",
		"-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(dir, "seg_%03d.jpg"),
		filepath.Join(dir, "index.m3u8"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, ffmpegPath, args...).CombinedOutput(); err != nil {
		t.Fatalf("generating HLS fixture: %v\n%s", err, out)
	}

	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(server.Close)
	return fixtureOrigin{server: server, path: "/index.m3u8", contentType: media.HLS}
}

// serveDemuxedHLSFixture generates an HLS stream whose video and audio are
// published as separate renditions, the shape a master with an EXT-X-MEDIA audio
// group resolves to: one playlist of video segments, one of audio segments, and
// neither is playable on its own.
func serveDemuxedHLSFixture(t *testing.T, ffmpegPath string) fixtureOrigin {
	t.Helper()
	dir := t.TempDir()
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=15:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-map", "0:v", "-map", "1:a",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-profile:v", "baseline",
		"-c:a", "aac", "-ac", "2", "-shortest",
		"-f", "hls",
		"-hls_time", "1",
		"-hls_list_size", "0",
		"-var_stream_map", "v:0,agroup:aud a:0,agroup:aud",
		filepath.Join(dir, "rendition_%v.m3u8"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, ffmpegPath, args...).CombinedOutput(); err != nil {
		t.Fatalf("generating demuxed HLS fixture: %v\n%s", err, out)
	}

	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(server.Close)
	return fixtureOrigin{
		server:      server,
		path:        "/rendition_0.m3u8",
		audioPath:   "/rendition_1.m3u8",
		contentType: media.HLS,
	}
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
