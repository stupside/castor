package execute

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

type fixedOrigin struct{ program media.Program }

func (o fixedOrigin) Resolve(context.Context, *source.Stream, source.Rendition, media.Bitrate) (source.Resolution, error) {
	return source.Resolution{Program: o.program.Clone()}, nil
}

type revisions struct{ actions []recovery.Action }

func (*revisions) Attempting(int) {}
func (r *revisions) Revising(action recovery.Action, _ string) {
	r.actions = append(r.actions, action)
}

func TestAHardwareEncoderThatFailsBeforeItsFirstOutputIsReplacedBySoftware(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	// MPEG-4 Part 2 is a picture the H.264-only renderer cannot take, so it is encoded.
	file := generateFixture(t, ffmpegPath, "movie.ts", 2, []string{"-c:v", "mpeg4", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, file) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL + "/movie.ts")
	stream := &source.Stream{URL: u, ContentType: media.MPEGTS}

	dev := &fakeDevice{caps: dlnaLike(), drain: true}
	cast := realCast(dev, ffmpegPath, ffprobePath)
	catalog, err := ffmpeg.NewCatalog(ffmpegPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	software := catalog.Software
	var asked, retired []string
	// An encoder ffmpeg has never heard of, offered as hardware ahead of everything: it fails as soon as ffmpeg starts.
	cast.Encoders = func(ctx context.Context, codec media.Codec) (ffmpeg.Encoder, bool) {
		if codec != media.CodecH264 || slices.Contains(retired, "h264_bogus") {
			return software(ctx, codec)
		}
		asked = append(asked, "h264_bogus")
		return ffmpeg.Encoder{Name: "h264_bogus", Codec: media.CodecH264, Hardware: true, GOPKeyframes: true, AverageRate: true}, true
	}
	cast.Software = catalog.Software
	cast.RetireEncoder = func(name string) { retired = append(retired, name) }
	turns := &revisions{}

	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	err = recovery.Cast(ctx, recovery.Intent{Candidates: []*source.Stream{stream}, Deadline: testReadDeadline, Turns: turns},
		cast, fixedOrigin{programFromStream(t, stream)})
	if err != nil {
		t.Fatalf("the cast failed instead of falling back to software: %v", err)
	}
	if !slices.Equal(turns.actions, []recovery.Action{recovery.SoftwareEncode}) {
		t.Fatalf("revisions %v, want exactly one software-encode", turns.actions)
	}
	if len(dev.served) == 0 {
		t.Fatal("the software retry served nothing")
	}
	if !slices.Equal(retired, []string{"h264_bogus"}) {
		t.Fatalf("retired %v: the encoder software proved at fault must be retired for the session", retired)
	}
	if len(asked) != 1 {
		t.Fatalf("the failed hardware encoder was planned %d times, want once", len(asked))
	}
	// Later casts no longer try it.
	if e, _ := cast.Encoders(ctx, media.CodecH264); e.Name == "h264_bogus" {
		t.Fatal("a retired encoder was offered to the next cast")
	}
}

func TestOnlyAHardwareEncoderThatFailedItselfIsBlamed(t *testing.T) {
	hardware := encodePlan{Video: codec.Encode(codec.VideoEncode{
		Encoder: ffmpeg.Encoder{Name: "h264_mediacodec", Codec: media.CodecH264, Hardware: true},
	})}
	for _, tt := range []struct {
		name  string
		err   error
		blame bool
	}{
		{name: "an encoder that would not start", err: errors.New("exit status 187"), blame: true},
		{name: "an encoder that ended with nothing playable", err: &health.Fault{Kind: health.Dead, Subject: "the stream output"}, blame: true},
		{name: "an output that went silent, as a stopped source leaves it", err: &health.Fault{Kind: health.Stalled, Subject: "the HLS playlist"}},
		{name: "an output that never kept pace", err: &health.Fault{Kind: health.Undeliverable, Subject: "the stream output"}},
		{name: "a cancelled cast", err: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, blamed := errors.AsType[*hardwareFailed](blameHardware(hardware, tt.err))
			if blamed != tt.blame {
				t.Fatalf("hardware blamed = %v, want %v", blamed, tt.blame)
			}
		})
	}
}

// streamingDevice reads what it is handed in the background, as a TV does, and says it ended a moment after its stream did.
type streamingDevice struct {
	caps  media.Capabilities
	mu    sync.Mutex
	loads int
	read  chan struct{}
}

func (d *streamingDevice) Play(ctx context.Context, p Playback) error {
	req, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodGet, p.URL.String(), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	read := make(chan struct{})
	d.mu.Lock()
	d.loads++
	d.read = read
	d.mu.Unlock()
	go func() {
		defer close(read)
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
	}()
	return nil
}

func (d *streamingDevice) AwaitEnd(ctx context.Context) error {
	d.mu.Lock()
	read := d.read
	d.mu.Unlock()
	select {
	case <-read:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	select {
	case <-time.After(time.Second):
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (*streamingDevice) Pause(context.Context) error        { return nil }
func (*streamingDevice) Resume(context.Context) error       { return nil }
func (d *streamingDevice) Capabilities() media.Capabilities { return d.caps }

// startsAt records where each attempt asks its program to begin, and whether it encodes in software.
type startsAt struct {
	recovery.Runner
	starts   []time.Duration
	software []bool
}

func (r *startsAt) Run(ctx context.Context, a recovery.Attempt) recovery.Outcome {
	r.starts, r.software = append(r.starts, a.Program.Start), append(r.software, a.SoftwareVideo)
	return r.Runner.Run(ctx, a)
}

func TestAHardwareEncoderThatFailsMidStreamIsReplacedBySoftwareWhereTheViewerWas(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	file := generateFixture(t, ffmpegPath, "movie.ts", 16, []string{"-c:v", "mpeg4", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, file) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL + "/movie.ts")
	stream := &source.Stream{URL: u, ContentType: media.MPEGTS}

	// A renderer that takes only MP4 is served by one encoder, which a failure ends: the device is handed a new stream.
	dev := &streamingDevice{caps: media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}}}
	cast := realCast(dev, ffmpegPath, ffprobePath)
	catalog, err := ffmpeg.NewCatalog(ffmpegPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	var retired []string
	// A "hardware" encoder that reads at playback pace and fails itself eight seconds in, when the picture it is fed changes size.
	failing := ffmpeg.Encoder{Name: "libx264", Codec: media.CodecH264, Hardware: true,
		InitArgs: []string{"-readrate", "1"},
		Filters:  []string{"scale=w='if(gt(t,8),160,320)':h=-2:eval=frame"},
		Flags:    []string{"-preset", "veryfast", "-pix_fmt", "yuv420p"}}
	cast.Encoders = func(ctx context.Context, codec media.Codec) (ffmpeg.Encoder, bool) {
		if codec != media.CodecH264 || len(retired) > 0 {
			return catalog.Software(ctx, codec)
		}
		return failing, true
	}
	cast.Software = catalog.Software
	cast.RetireEncoder = func(name string) { retired = append(retired, name) }
	turns := &revisions{}
	run := &startsAt{Runner: cast}

	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	err = recovery.Cast(ctx, recovery.Intent{Candidates: []*source.Stream{stream}, Deadline: testReadDeadline, Turns: turns},
		run, fixedOrigin{programFromStream(t, stream)})
	if err != nil {
		t.Fatalf("the cast failed instead of carrying on in software: %v", err)
	}
	if !slices.Equal(turns.actions, []recovery.Action{recovery.SoftwareEncode}) {
		t.Fatalf("revisions %v, want exactly one software-encode", turns.actions)
	}
	if !slices.Equal(run.software, []bool{false, true}) {
		t.Fatalf("attempts in software %v, want the hardware one then software", run.software)
	}
	t.Logf("the encoder failed eight seconds in; the software attempt began at %v", run.starts[1])
	if at := run.starts[1]; at < time.Second || at > 12*time.Second {
		t.Fatalf("the software attempt began at %v, want where the viewer had got to before the encoder failed", at)
	}
	if dev.loads != 2 {
		t.Fatalf("the device was handed %d streams, want the failed one and its replacement", dev.loads)
	}
	if !slices.Equal(retired, []string{"libx264"}) {
		t.Fatalf("retired %v, want the failed hardware encoder retired once software played", retired)
	}
}

func TestAnEncoderIsBlamedOnlyForItsOwnErrors(t *testing.T) {
	for _, tt := range []struct {
		name  string
		lines []string
		blame bool
	}{
		{name: "the codec refused a frame", lines: []string{
			"[libx264 @ 0x5cbf] [error] Input picture width (640) is greater than stride (320)",
			"[vost#0:0/libx264 @ 0x5cbf] [enc:libx264 @ 0x5cbf] [error] Error submitting video frame to the encoder",
		}, blame: true},
		{name: "MediaCodec stopped", lines: []string{
			"[h264_mediacodec @ 0xb400] [error] MediaCodec configure failed, Generic error in an external library",
		}, blame: true},
		{name: "the source went away", lines: []string{
			"[in#0/mpegts @ 0x5cbf] [error] Error during demuxing: I/O error",
			"[http @ 0x5cbf] [error] Will reconnect at 1024 in 0 second(s), error=Connection reset by peer.",
		}},
		{name: "the device closed the delivery", lines: []string{
			"[vost#0:0/libx264 @ 0x5cbf] [error] Error submitting a packet to the muxer: Broken pipe",
			"[out#0/mpegts @ 0x5cbf] [error] Error muxing a packet",
		}},
		{name: "the encoder only warned", lines: []string{
			"[libx264 @ 0x5cbf] [warning] VBV underflow",
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := failedItself("libx264", tt.lines) || failedItself("h264_mediacodec", tt.lines); got != tt.blame {
				t.Fatalf("blamed = %v, want %v", got, tt.blame)
			}
		})
	}
}

// quiet is a producer that left no log.
type quiet struct{}

func (quiet) Done() <-chan struct{} { return nil }
func (quiet) Evidence() []string    { return nil }

func TestAHardwareEncodeThatCrashedIsBlamedOnTheEncoder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("crashes are told by their signal")
	}
	hardware := encodePlan{Video: codec.Encode(codec.VideoEncode{
		Encoder: ffmpeg.Encoder{Name: "h264_mediacodec", Codec: media.CodecH264, Hardware: true},
	})}
	for _, tt := range []struct {
		name  string
		sig   string
		blame bool
	}{
		{name: "an aborted ffmpeg", sig: "ABRT", blame: true},
		{name: "a bad memory access", sig: "SEGV", blame: true},
		{name: "an ffmpeg stopped from outside", sig: "TERM"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			died := fmt.Errorf("encoder: %w", exec.Command("sh", "-c", "kill -"+tt.sig+" $$").Run())
			if _, blamed := errors.AsType[*hardwareFailed]((&coordinator{}).encoderFailed(died, hardware, quiet{})); blamed != tt.blame {
				t.Fatalf("hardware blamed = %v, want %v", blamed, tt.blame)
			}
		})
	}
}
