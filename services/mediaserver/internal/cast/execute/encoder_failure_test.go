package execute

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// fetchingDevice fetches what it was handed in the background and ends once it took it all.
type fetchingDevice struct {
	caps media.Capabilities
	// slow reads the first play slowly; held stops reading it for a while, as a pause does.
	slow, held bool
	// stop ends the first play this long in, as a viewer does.
	stop  time.Duration
	mu    sync.Mutex
	ended chan struct{}
	plays int
}

func (d *fetchingDevice) Play(ctx context.Context, p Playback) error {
	ended := make(chan struct{})
	d.mu.Lock()
	d.ended = ended
	d.plays++
	first := d.plays == 1
	d.mu.Unlock()
	go func() {
		defer close(ended)
		if first && d.stop > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d.stop)
			defer cancel()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL.String(), nil)
		if err != nil {
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		for buf, held := make([]byte, 16<<10), first && d.held; ; held = false {
			if _, err := resp.Body.Read(buf); err != nil {
				return
			}
			switch {
			case held:
				time.Sleep(health.Warmup + 2*time.Second)
			case first && d.slow:
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
	return nil
}

func (d *fetchingDevice) AwaitEnd(ctx context.Context) error {
	d.mu.Lock()
	ended := d.ended
	d.mu.Unlock()
	select {
	case <-ended:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (d *fetchingDevice) Capabilities() media.Capabilities { return d.caps }
func (*fetchingDevice) Pause(context.Context) error        { return nil }
func (*fetchingDevice) Resume(context.Context) error       { return nil }

// revisions hears the actions a cast revised with.
type revisions struct {
	mu      sync.Mutex
	actions []recovery.Action
}

func (*revisions) Attempting(int) {}
func (r *revisions) Revising(action recovery.Action, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions = append(r.actions, action)
}

// faultCast casts links through testdata/ffmpeg-fault, with encoder as the hardware H.264 encoder.
func faultCast(t *testing.T, dev Device, encoder string, links ...*source.Stream) (actions []recovery.Action, ran []recovery.Attempt, outcomes []recovery.Outcome, err error) {
	t.Helper()
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	faulty, err := filepath.Abs("testdata/ffmpeg-fault")
	if err != nil {
		t.Fatal(err)
	}
	c := realCast(dev, faulty, ffprobePath)
	software := ffmpeg.Encoders(ffmpegPath, nil)
	c.Encoders = func(ctx context.Context, codec media.Codec, soft bool) (ffmpeg.Encoder, bool) {
		if soft || codec != media.CodecH264 {
			return software(ctx, codec, true)
		}
		return ffmpeg.Encoder{Name: encoder, Codec: media.CodecH264, Hardware: true, Flags: []string{"-pix_fmt", "yuv420p"}}, true
	}
	var mu sync.Mutex
	runner := runnerFunc(func(ctx context.Context, a recovery.Attempt) recovery.Outcome {
		out := c.Run(ctx, a)
		mu.Lock()
		ran, outcomes = append(ran, a), append(outcomes, out)
		mu.Unlock()
		return out
	})
	turns := &revisions{}
	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	err = recovery.Cast(ctx, recovery.Intent{Candidates: links, Deadline: testReadDeadline, Turns: turns}, runner, linkOrigin{t})
	return turns.actions, ran, outcomes, err
}

// linkOrigin resolves every link to its own program.
type linkOrigin struct{ t *testing.T }

func (o linkOrigin) Resolve(_ context.Context, s *source.Stream, _ source.Rendition, _ media.Bitrate) (source.Resolution, error) {
	return source.Resolution{Program: programFromStream(o.t, s)}, nil
}

// sourceOf serves file as an MPEG-TS source.
func sourceOf(t *testing.T, file string) *source.Stream {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, file) }))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL + "/movie.ts")
	return &source.Stream{URL: u, ContentType: media.MPEGTS}
}

func TestAHardwareEncoderThatFailsOnRealInputIsReplacedBySoftware(t *testing.T) {
	t.Parallel()
	ffmpegPath, _ := requireFFmpegTools(t)
	// MPEG-4 forces an encode; noise makes it outweigh the socket buffers.
	stream := sourceOf(t, generateFixture(t, ffmpegPath, "movie.ts", 60, []string{"-vf", "noise=alls=40:allf=t", "-c:v", "mpeg4", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"}))

	for _, tt := range []struct {
		name    string
		encoder string
		caps    media.Capabilities
		// resumed: the retry starts past 0; wedged: the first attempt ended Wedged.
		slow, held, resumed, wedged bool
		want                        []recovery.Action
	}{
		{"one that will not start", "h264_bogus", dlnaLike(), false, false, false, false, []recovery.Action{recovery.SoftwareEncode}},
		{"one that will not start alone", "h264_bogus", mp4Only(), false, false, false, false, []recovery.Action{recovery.SoftwareEncode}},
		{"one that makes what the plan refused", "mpeg2video", dlnaLike(), false, false, false, false, []recovery.Action{recovery.SoftwareEncode}},
		{"one that crashes while playing", "h264_crash", dlnaLike(), true, false, true, false, []recovery.Action{recovery.SoftwareEncode}},
		{"one that hangs while playing", "h264_hang", dlnaLike(), true, false, true, true, []recovery.Action{recovery.SoftwareEncode}},
		{"one held back by a device that stopped taking it", "libx264", dlnaLike(), false, true, false, false, nil},
		// A single encoder is judged before any device fetches it.
		{"one that never makes its first byte", "h264_mute", mp4Only(), false, false, false, true, []recovery.Action{recovery.SoftwareEncode}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			actions, ran, outcomes, err := faultCast(t, &fetchingDevice{caps: tt.caps, slow: tt.slow, held: tt.held}, tt.encoder, stream)
			if err != nil {
				t.Fatalf("the cast failed instead of going on in software: %v", err)
			}
			if !slices.Equal(actions, tt.want) {
				t.Fatalf("revisions %v, want %v", actions, tt.want)
			}
			if len(ran) < 2 {
				return
			}
			if next := ran[1]; !next.Software || next.Program.Start > 0 != tt.resumed {
				t.Errorf("the retry: software %t from %s, want software resumed %t", next.Software, next.Program.Start, tt.resumed)
			}
			if wedged := outcomes[0].Evidence.Verdict == health.Wedged; wedged != tt.wedged {
				t.Errorf("the first attempt ended on %v, want wedged %t", outcomes[0].Err, tt.wedged)
			}
		})
	}
}

// An encoder failing on its own leaves its lines to the cast's fault, which quotes them beside its error, never in it too.
func TestAnEncodersOwnFailureLeavesItsLinesToTheFault(t *testing.T) {
	t.Parallel()
	ffmpegPath, _ := requireFFmpegTools(t)
	stream := sourceOf(t, generateFixture(t, ffmpegPath, "movie.ts", 10, []string{"-c:v", "mpeg4", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"}))
	_, _, outcomes, _ := faultCast(t, &fetchingDevice{caps: dlnaLike()}, "h264_bogus", stream)
	if len(outcomes) == 0 {
		t.Fatal("no attempt ended")
	}
	first := outcomes[0]
	if said := strings.Join(first.Evidence.Lines, " "); !strings.Contains(said, "Unknown encoder") || strings.Contains(first.Err.Error(), "Unknown encoder") {
		t.Errorf("the encoder said %q and its error reads %q, want its lines beside the error only", said, first.Err)
	}
}

func TestAHealthyEncodeTheViewerStopsEndsCleanly(t *testing.T) {
	t.Parallel()
	ffmpegPath, _ := requireFFmpegTools(t)
	// No sound and a 30s GOP: the buffer's index moves only at keyframes.
	sparse := sourceOf(t, generateFixture(t, ffmpegPath, "sparse.ts", 60, []string{"-c:v", "mpeg4", "-g", "450", "-an", "-f", "mpegts"}))
	for _, tt := range []struct {
		name string
		caps media.Capabilities
	}{
		{"made by one encoder", mp4Only()},
		{"made by encoders in turn", dlnaLike()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Paced, so the encoder still has input when the viewer stops.
			actions, _, _, err := faultCast(t, &fetchingDevice{caps: tt.caps, stop: health.Warmup + 2*time.Second}, "h264_paced", sparse)
			if err != nil || len(actions) > 0 {
				t.Fatalf("the cast ended on %v after revisions %v, want a clean end", err, actions)
			}
		})
	}
}

type runnerFunc func(context.Context, recovery.Attempt) recovery.Outcome

func (f runnerFunc) Run(ctx context.Context, a recovery.Attempt) recovery.Outcome { return f(ctx, a) }

// mp4Only takes MP4 alone, which one encoder makes for the whole cast.
func mp4Only() media.Capabilities {
	return media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}}
}

func TestAnOutputTheReceiverRefusesIsTheEncodesOnlyWhereItEncodedWhatWasRefused(t *testing.T) {
	hardware := codec.Encode(codec.VideoEncode{Encoder: ffmpeg.Encoder{Name: "h264_videotoolbox", Hardware: true}})
	sound := codec.Encode(codec.AudioEncode{Codec: media.CodecAAC})
	refused := func(axes media.Axes) error { return fmt.Errorf("validating: %w", &encoding.Mismatch{Refused: axes}) }
	for _, tt := range []struct {
		name     string
		err      error
		opts     encoding.EncodeOptions
		failed   bool
		onDevice bool
	}{
		{"a picture made on hardware", refused(media.Axes{Video: true}), encoding.EncodeOptions{Video: hardware, Audio: codec.CopyAudio()}, true, true},
		{"a picture copied as it was", refused(media.Axes{Video: true}), encoding.EncodeOptions{Video: codec.CopyVideo(), Audio: sound}, false, false},
		{"a sound encoded beside a hardware picture", refused(media.Axes{Audio: true}), encoding.EncodeOptions{Video: hardware, Audio: sound}, true, false},
		{"both halves", refused(media.Axes{Video: true, Audio: true}), encoding.EncodeOptions{Video: hardware, Audio: sound}, false, false},
		{"a validation that ran out of time", fmt.Errorf("validating encoded output: %w", context.DeadlineExceeded), encoding.EncodeOptions{Video: hardware, Audio: sound}, false, false},
	} {
		failed, ok := errors.AsType[*encoderFailed](encodeFault(tt.err, tt.opts))
		if ok != tt.failed || ok && failed.hardware != tt.onDevice {
			t.Errorf("%s: encode failed %t (hardware %t), want %t (hardware %t)", tt.name, ok, ok && failed.hardware, tt.failed, tt.onDevice)
		}
	}
}

func TestARemuxOfDeadLinksIsNotBlamedOnItsEncoder(t *testing.T) {
	t.Parallel()
	requireFFmpegTools(t)
	gone := httptest.NewServer(http.HandlerFunc(http.NotFound))
	t.Cleanup(gone.Close)
	dead := func(path string) *source.Stream {
		u, _ := url.Parse(gone.URL + path)
		return &source.Stream{URL: u, ContentType: media.MPEGTS}
	}
	dev := &fakeDevice{caps: media.Capabilities{SelfFetch: true, Formats: []media.FormatSupport{{Transport: media.TransportMPEGTS, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}}, drain: true}
	actions, _, _, err := faultCast(t, dev, "h264_bogus", dead("/one.ts"), dead("/two.ts"))
	if err == nil || !slices.Equal(actions, []recovery.Action{recovery.SwitchCandidate}) {
		t.Fatalf("the cast ended on %v after revisions %v, want a refusal after one switch of link", err, actions)
	}
}

func TestAnExitIsTheEncodesOnlyWhereItCannotBeTheSources(t *testing.T) {
	for _, tt := range []struct {
		name     string
		script   string
		buffered bool
		own      bool
	}{
		{"an exit over castor's buffer", "exit 3", true, true},
		{"an exit over the source", "exit 3", false, false},
		{"a signal of its own over the source", "kill -SEGV $$", false, true},
	} {
		proc, err := ffmpeg.Start(t.Context(), "/bin/sh", ffmpeg.Command{Args: []string{"-c", tt.script}}, ffmpeg.Options{})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, proc.Stdout)
		if _, own := errors.AsType[*encoderFailed](encoderResult(t.Context(), proc, false, tt.buffered)); own != tt.own {
			t.Errorf("%s: the encode's own failure %t, want %t", tt.name, own, tt.own)
		}
	}
}
