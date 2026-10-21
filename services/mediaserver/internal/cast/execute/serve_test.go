package execute

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/probe"
)

type artifactMechanism struct {
	fakeMechanism
	path string
}

func (m *artifactMechanism) Artifact() output.Artifact {
	return output.Artifact{Path: m.path}
}

func TestOutputRetainsTracksDiscoveredByTheBufferedRead(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	for _, missing := range []string{"video", "audio"} {
		t.Run(missing, func(t *testing.T) {
			args := []string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-f", "mpegts"}
			if missing == "video" {
				args = append(args, "-vn")
			} else {
				args = append(args, "-an")
			}
			path := generateFixture(t, ffmpegPath, "output.ts", 1, args)
			s := testPreparation(t.Context(), Cast{Probes: probe.FFprobe(ffprobePath)}, recovery.Attempt{})
			t.Cleanup(func() { _ = s.releases.release() })
			p := took(t, over)
			close(p.buf.reader.done)
			d := delivery{sink: &artifactMechanism{path: path}, output: p.buf.reader}
			opts := encodePlan{
				Format:   encoding.SpoolFormat,
				delivery: output.Selection{Capabilities: dlnaLike()},
			}
			// A sparse later probe and an unmeasured optional source cannot erase
			// tracks already established by the read into the buffer.
			if err := s.validateOutput(d, opts, feed{}); err != nil {
				t.Fatalf("single-track control artifact is not playable: %v", err)
			}
			p.buf.expected = media.TrackRequirements{Video: true, Audio: true}
			if err := s.validateOutput(d, opts, feed{buffered: p.buf}); err == nil {
				t.Fatalf("output lost buffered %s without failing validation", missing)
			}
		})
	}
}

// playing is a buffered cast handed to a device, whose delivery ends as its mechanism says.
type playing struct {
	buf  *buffered
	dev  Device
	mech *fakeMechanism
}

func (p playing) delivery() delivery { return delivery{sink: p.mech, output: p.buf.reader} }

// took returns a playing buffered cast whose delivery ends as wait says.
func took(t *testing.T, wait func(ctx context.Context) error) playing {
	t.Helper()
	return playing{buf: gateFixture(t), dev: observedDevice{wait: blocking}, mech: &fakeMechanism{wait: wait}}
}

// blocking is a delivery that never finishes.
func blocking(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// over is a delivery that has already run its course.
func over(context.Context) error { return nil }

// fakeMechanism is a delivery whose answers a test states outright.
type fakeMechanism struct {
	wait     func(ctx context.Context) error
	audience health.Audience
	settled  error
	asked    atomic.Int64
}

func (m *fakeMechanism) Path() string                                      { return "" }
func (m *fakeMechanism) Handler() http.Handler                             { return http.NotFoundHandler() }
func (m *fakeMechanism) Wait(ctx context.Context, _ <-chan struct{}) error { return m.wait(ctx) }
func (m *fakeMechanism) Audience() health.Audience                         { return m.audience }
func (m *fakeMechanism) Settled(media.Progress) error                      { m.asked.Add(1); return m.settled }
func (m *fakeMechanism) Close() error                                      { return nil }

func (m *fakeMechanism) Artifact() output.Artifact {
	return output.Artifact{Subject: "a stated delivery", Landed: func() (int64, error) { return 8 << 20, nil }}
}

// observedDevice is a device whose playback lifecycle a case states.
type observedDevice struct {
	wait func(context.Context) error
}

func (observedDevice) Play(context.Context, *url.URL, string, []media.Caption) error { return nil }
func (observedDevice) Capabilities() media.Capabilities                              { return media.Capabilities{} }
func (r observedDevice) AwaitEnd(ctx context.Context) error                          { return r.wait(ctx) }

func TestAPlayingRemuxIsWatchedOverItsEncoder(t *testing.T) {
	p := took(t, blocking)
	p.mech.audience = stoppedDevice{last: time.Now().Add(-health.StallWindow - time.Second)}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := superviseFixture(ctx, p.dev, p.delivery(), feed{})
	fault, ok := errors.AsType[*health.Fault](err)
	if !ok {
		t.Fatal("a remux whose device took nothing was never judged in flight")
	}
	if fault.Kind != health.Unfetched {
		t.Errorf("verdict %s, want %s judged on castor's own encoder", fault.Kind, health.Unfetched)
	}
}

func TestADeviceThatWentAwayIsReportedAsItselfNotAsTeardown(t *testing.T) {
	gone := &media.Gone{Device: "Living Room", Err: errors.New("no route to host")}
	p := took(t, blocking)
	p.dev = observedDevice{wait: func(context.Context) error { return gone }}

	_, err := superviseFixture(t.Context(), p.dev, p.delivery(), feed{buffered: p.buf})
	if away, ok := errors.AsType[*media.Gone](err); !ok || away != gone {
		t.Fatalf("playback = %v, want the device's own account of having gone away", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("playback = %v, teardown cancellation reported as another failure", err)
	}
}

func TestOnlyADeliveryThatRanItsCourseIsAskedWhetherTheDeviceTookIt(t *testing.T) {
	short := errors.New("the device was handed a fraction of what this cast produced")
	inFlight := func(ctx context.Context, p playing) error {
		_, err := superviseFixture(ctx, p.dev, p.delivery(), feed{buffered: p.buf})
		return err
	}

	t.Run("a delivery that ran its course is asked", func(t *testing.T) {
		p := took(t, over)
		p.mech.settled = short
		if err := inFlight(t.Context(), p); !errors.Is(err, short) {
			t.Fatalf("the cast = %v, want the delivery's own account of what the device took", err)
		}
	})

	t.Run("a cast the user stopped is not asked", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		p := took(t, blocking)
		p.mech.settled = short
		if err := inFlight(ctx, p); !errors.Is(err, context.Canceled) || errors.Is(err, short) {
			t.Fatalf("the cast = %v, want the cancellation alone", err)
		}
		if asked := p.mech.asked.Load(); asked != 0 {
			t.Errorf("a cancelled cast was asked %d time(s) what its device took", asked)
		}
	})
}

func TestADeliveryPublishesItsOwnArtifactsAndNothingElseOfTheCasts(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "spool.ts"), []byte("the cast's own buffer"), 0o600); err != nil {
		t.Fatal(err)
	}

	d := openFixture(t, media.HLS, workDir)
	if err := awaitArtifact(t.Context(), d); err != nil {
		t.Fatalf("the playlist never appeared: %v", err)
	}
	leak := d.URL()
	leak.Path = "/spool.ts"
	if got := statusOf(t, leak); got == http.StatusOK {
		t.Errorf("GET %s = 200: the delivery publishes the cast's private files", leak.Path)
	}
	if got := statusOf(t, d.URL()); got != http.StatusOK {
		t.Errorf("GET playlist = %d, so this is a broken server rather than isolation", got)
	}
}

// TestARelayedCastIsOpenedOverItsRead: with no encoder, the read is the producer whose end the opening judges.
func TestARelayedCastIsOpenedOverItsRead(t *testing.T) {
	s, buf := servingAttempt(t, ""), gateFixture(t)
	buf.reader.spool.CloseWrite(nil)
	close(buf.reader.done)
	s.capabilities = observedDevice{}.Capabilities()
	d, err := s.produce(t.TempDir(), feed{buffered: buf}, testEncoding(t, verbatim()), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Well inside the first-bytes grace, so only the read's own end can decide the opening.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err = awaitArtifact(ctx, d)
	if fault, ok := errors.AsType[*health.Fault](err); !ok || fault.Kind != health.Dead {
		t.Fatalf("a read that ended having buffered nothing opened as %v, want a %s verdict", err, health.Dead)
	}
}

// TestARelayedCastFailsWithItsRead: the read writes the served bytes, so its failure is the cast's as an encoder's was.
func TestARelayedCastFailsWithItsRead(t *testing.T) {
	failed := errors.New("upstream pull: the origin refused a segment on every retry")
	s, buf := servingAttempt(t, ""), gateFixture(t)
	buf.reader.err = failed
	buf.reader.spool.CloseWrite(failed)
	close(buf.reader.done)
	s.capabilities = observedDevice{}.Capabilities()
	d, err := s.produce(t.TempDir(), feed{buffered: buf}, testEncoding(t, verbatim()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := awaitArtifact(t.Context(), d); !errors.Is(err, failed) {
		t.Fatalf("readiness = %v, want upstream failure", err)
	}
}

// verbatim is an encode that would rewrite the buffer unchanged, so the buffer is served instead.
func verbatim() encoding.EncodeOptions {
	return encoding.EncodeOptions{
		Input:  encoding.FromPipe(fetch.Pace{}),
		Format: encoding.SpoolFormat,
		Video:  codec.CopyVideo(),
		Audio:  codec.CopyAudio(),
	}
}

func statusOf(t *testing.T, u *url.URL) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// TestTeardownStopsAnEncoderParkedOnAnInputThatWentQuiet: castor's own kill is not an encoder failure.
func TestTeardownStopsAnEncoderParkedOnAnInputThatWentQuiet(t *testing.T) {
	ffmpegPath, _ := requireFFmpegTools(t)
	format, ok := container.For(media.MPEGTS)
	if !ok {
		t.Fatal("the format registry cannot produce mpegts")
	}
	head := programHead(t, ffmpegPath)
	copying := func(input encoding.EncodeInput) encoding.EncodeOptions {
		return encoding.EncodeOptions{
			Format: format,
			Input:  input,
			Probe:  media.ProbeInfo{VideoCodec: media.CodecH264, AudioCodec: media.CodecAAC},
			Video:  codec.CopyVideo(),
			Audio:  codec.CopyAudio(),
		}
	}

	for _, tt := range []struct {
		name  string
		setup func(t *testing.T, s *preparation, work string) (feed, encoding.EncodeOptions)
	}{{
		name: "reading a buffer nothing will grow",
		setup: func(t *testing.T, s *preparation, work string) (feed, encoding.EncodeOptions) {
			sp, err := storage.NewSpool(filepath.Join(work, "spool.ts"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { sp.CloseWrite(nil) })
			if _, err := sp.Write(head); err != nil {
				t.Fatal(err)
			}
			opts := copying(encoding.FromPipe(fetch.Pace{}))
			// A buffer copied whole into its own container is served with no encoder at all.
			opts.Audio = codec.Encode(codec.AudioEncode{Codec: media.CodecAAC})
			return feed{buffered: &buffered{reader: &pull{spool: sp, done: make(chan struct{})}}}, opts
		},
	}, {
		name: "reading an origin that went quiet",
		setup: func(t *testing.T, s *preparation, _ string) (feed, encoding.EncodeOptions) {
			return feed{}, copying(encoding.FromSource(programSourceWithin(t, quietOrigin(t, head), media.MPEGTS, time.Hour)))
		},
	}} {
		t.Run(tt.name, func(t *testing.T) {
			s, work := servingAttempt(t, ffmpegPath), t.TempDir()
			s.capabilities = probingDevice{}.Capabilities()
			f, opts := tt.setup(t, s, work)
			d, err := s.produce(work, f, testEncoding(t, opts), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := awaitArtifact(t.Context(), d); err != nil {
				t.Fatal(err)
			}
			if err := hand(t.Context(), probingDevice{}, d.URL(), opts.Format.ContentType, nil, false); err != nil {
				t.Fatal(err)
			}

			const teardown = 30 * time.Second
			done := make(chan error, 1)
			go func() { done <- s.releases.release() }()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("the teardown reported %v for an encoder castor killed itself", err)
				}
			case <-time.After(teardown):
				t.Fatalf("the teardown did not return within %s", teardown)
			}
		})
	}
}

// quietOrigin hands over the head of a program and then goes quiet without ending the response.
func quietOrigin(t *testing.T, head []byte) *url.URL {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", media.MPEGTS)
		if _, err := w.Write(head); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL + "/program.ts")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// programHead is the first two thirds of a program, so an encoder reading it never reaches its end.
func programHead(t *testing.T, ffmpegPath string) []byte {
	t.Helper()
	program, err := os.ReadFile(generateFixture(t, ffmpegPath, "program.ts", 12,
		[]string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"}))
	if err != nil {
		t.Fatal(err)
	}
	return program[:len(program)*2/3]
}

// servingAttempt is the attempt a delivery is opened in, released when the test ends.
func servingAttempt(t *testing.T, ffmpegPath string) *preparation {
	t.Helper()
	s := testPreparation(t.Context(),
		Cast{FFmpegPath: ffmpegPath, Encoders: ffmpeg.Encoders(ffmpegPath), InputArgs: formats.InputArgs, Outputs: testOutputs(), Publisher: loopback{}, Timelines: direct{}},
		recovery.Attempt{})
	t.Cleanup(func() { _ = s.releases.release() })
	return s
}

// openFixture opens one real delivery of a real encode over a real origin, in workDir.
func openFixture(t *testing.T, contentType, workDir string) delivery {
	t.Helper()
	ffmpegPath, _ := requireFFmpegTools(t)
	origin := serveFixture(t, ffmpegPath)
	s := servingAttempt(t, ffmpegPath)
	selected, err := testOutputs().Select(chromecastLike(contentType), nil)
	if err != nil {
		t.Fatal(err)
	}
	format := selected.Plan.Format()
	opts := encoding.EncodeOptions{
		Format: format,
		Input:  encoding.FromSource(programSourceWithin(t, origin.stream().URL, media.MP4, 30*time.Second)),
		Probe:  media.ProbeInfo{VideoCodec: media.CodecH264},
		Video:  codec.CopyVideo(),
		Audio:  codec.Encode(codec.AudioEncode{Codec: media.CodecAAC}),
	}
	s.capabilities = probingDevice{}.Capabilities()
	d, err := s.produce(workDir, feed{}, testEncoding(t, opts), nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// probingDevice accepts the URL, probes it like a real firmware, and never takes a byte.
type probingDevice struct{}

func (probingDevice) Play(ctx context.Context, streamURL *url.URL, _ string, _ []media.Caption) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, streamURL.String(), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (probingDevice) AwaitEnd(ctx context.Context) error {
	<-ctx.Done()
	return context.Cause(ctx)
}

func (probingDevice) Capabilities() media.Capabilities { return media.Capabilities{} }

func programSourceWithin(t *testing.T, sourceURL *url.URL, contentType string, rwTimeout time.Duration) encoding.ProgramSource {
	t.Helper()
	program, err := media.NewProgram(media.Program{
		Inputs: []media.Input{{ID: media.PrimaryInputID, URL: sourceURL, ContentType: contentType}},
		Tracks: []media.TrackRef{
			{Input: media.PrimaryInputID, Kind: media.TrackVideo, Optional: true},
			{Input: media.PrimaryInputID, Kind: media.TrackAudio, Optional: true},
		},
		ClockInput: media.PrimaryInputID,
		EndPolicy:  media.EndAtLongest,
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := encoding.NewProgramSource(program, map[media.InputID]fetch.Policy{
		media.PrimaryInputID: fetch.For(media.Fetch{}, rwTimeout),
	}, ffmpeg.Binary{}, formats.InputArgs)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func (observedDevice) Pause(context.Context) error  { return nil }
func (observedDevice) Resume(context.Context) error { return nil }

func (probingDevice) Pause(context.Context) error  { return nil }
func (probingDevice) Resume(context.Context) error { return nil }

func testEncoding(t *testing.T, opts encoding.EncodeOptions) encodePlan {
	t.Helper()
	selected, err := testOutputs().Select(chromecastLike(opts.Format.ContentType), nil)
	if err != nil {
		t.Fatal(err)
	}
	return encodePlan{EncodeOptions: opts, delivery: selected}
}
