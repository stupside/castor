package execute

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/cast/splice"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// recordingDevice reads what it is handed in the background at up to rate bytes a second (0 for as fast as it comes), keeps it, and says it ended a moment after its stream did.
type recordingDevice struct {
	caps  media.Capabilities
	rate  int
	mu    sync.Mutex
	loads int
	got   []byte
	read  chan struct{}
}

func (d *recordingDevice) Play(ctx context.Context, p Playback) error {
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
		chunk := make([]byte, 16<<10)
		for {
			n, err := resp.Body.Read(chunk)
			d.mu.Lock()
			d.got = append(d.got, chunk[:n]...)
			d.mu.Unlock()
			if err != nil {
				return
			}
			if d.rate > 0 {
				time.Sleep(time.Duration(float64(n) / float64(d.rate) * float64(time.Second)))
			}
		}
	}()
	return nil
}

func (d *recordingDevice) AwaitEnd(ctx context.Context) error {
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

func (*recordingDevice) Pause(context.Context) error        { return nil }
func (*recordingDevice) Resume(context.Context) error       { return nil }
func (d *recordingDevice) Capabilities() media.Capabilities { return d.caps }

// steps is the largest step forward and back between consecutive times.
func steps(times []time.Duration) (forward, back time.Duration) {
	for i := 1; i < len(times); i++ {
		d := times[i] - times[i-1]
		forward, back = max(forward, d), max(back, -d)
	}
	return forward, back
}

// tsOpenings is the pictures and sounds that open in an MPEG-TS stream, in the order they come.
func tsOpenings(ts []byte) (pictures, sounds []time.Duration) {
	spoolFraming().Scanner().Scan(ts, func(e splice.Event) {
		if e.Kind == splice.Sound {
			sounds = append(sounds, e.PTS)
		} else {
			pictures = append(pictures, e.PTS)
		}
	})
	return pictures, sounds
}

// chainFixture serves a movie a DLNA renderer is sent through an encoder chain: buffered, indexed and carried as MPEG-TS.
func chainFixture(t *testing.T, ffmpegPath string, seconds int) *source.Stream {
	t.Helper()
	file := generateFixture(t, ffmpegPath, "movie.ts", seconds, []string{"-c:v", "mpeg4", "-g", "15", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, file) }))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL + "/movie.ts")
	return &source.Stream{URL: u, ContentType: media.MPEGTS}
}

// failingHardware is libx264 posing as a hardware encoder without B-frames, as MediaCodec runs, with filters that make it fail.
func failingHardware(filters ...string) ffmpeg.Encoder {
	return ffmpeg.Encoder{Name: "libx264", Codec: media.CodecH264, Hardware: true, Filters: filters,
		Flags: []string{"-preset", "veryfast", "-pix_fmt", "yuv420p", "-bf", "0"}}
}

// castWith casts stream to dev with encoder offered as the hardware one until retired, reporting revisions, retirements and attempts.
func castWith(t *testing.T, dev Device, stream *source.Stream, encoder ffmpeg.Encoder) ([]recovery.Action, []string, *startsAt, error) {
	t.Helper()
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	cast := realCast(dev, ffmpegPath, ffprobePath)
	catalog, err := ffmpeg.NewCatalog(ffmpegPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var retired []string
	cast.Encoders = func(ctx context.Context, c media.Codec) (ffmpeg.Encoder, bool) {
		mu.Lock()
		gone := len(retired) > 0
		mu.Unlock()
		if c != media.CodecH264 || gone {
			return catalog.Software(ctx, c)
		}
		return encoder, true
	}
	cast.Software = catalog.Software
	cast.RetireEncoder = func(name string) {
		mu.Lock()
		retired = append(retired, name)
		mu.Unlock()
	}
	turns := &revisions{}
	run := &startsAt{Runner: cast}
	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	err = recovery.Cast(ctx, recovery.Intent{Candidates: []*source.Stream{stream}, Deadline: testReadDeadline, Turns: turns}, run, fixedOrigin{programFromStream(t, stream)})
	mu.Lock()
	defer mu.Unlock()
	return turns.actions, slices.Clone(retired), run, err
}

// shorterHangWindows lets the tests tell a hung encoder within seconds.
func shorterHangWindows(t *testing.T) {
	chain, single := chainHangWindow, hangWindow
	chainHangWindow, hangWindow = 2*time.Second, 3*time.Second
	t.Cleanup(func() { chainHangWindow, hangWindow = chain, single })
}

func TestAHardwareEncoderThatFailsInAChainIsCarriedOnInSoftwareInTheSameStream(t *testing.T) {
	ffmpegPath, _ := requireFFmpegTools(t)
	shorterHangWindows(t)
	for _, tt := range []struct {
		name    string
		encoder ffmpeg.Encoder
	}{
		{name: "it dies", encoder: failingHardware("scale=w='if(gt(t,8),160,320)':h=-2:eval=frame")},
		{name: "it hangs", encoder: failingHardware("sendcmd=c='8.0 realtime speed 0.000001'", "realtime=limit=100000")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dev := &recordingDevice{caps: dlnaLike()}
			actions, retired, _, err := castWith(t, dev, chainFixture(t, ffmpegPath, 16), tt.encoder)
			if err != nil {
				t.Fatalf("the cast failed instead of carrying on: %v", err)
			}
			if len(actions) != 0 || dev.loads != 1 {
				t.Fatalf("revisions %v over %d streams, want the delivery carried on in the one stream", actions, dev.loads)
			}
			if !slices.Equal(retired, []string{"libx264"}) {
				t.Fatalf("retired %v, want the failed hardware encoder retired", retired)
			}
			for at := 0; at+tsPacket <= len(dev.got); at += tsPacket {
				if dev.got[at] != 0x47 {
					t.Fatalf("the stream loses its packet alignment at byte %d", at)
				}
			}
			pictures, sounds := tsOpenings(dev.got)
			// Pictures come in decode order; the device shows them in presentation order.
			shown := slices.Sorted(slices.Values(pictures))
			frame := time.Second / 15
			gap, repeats := time.Duration(0), 0
			for i := 1; i < len(shown); i++ {
				gap = max(gap, shown[i]-shown[i-1])
				if shown[i] == shown[i-1] {
					repeats++
				}
			}
			if gap > frame*3/2 || repeats > 1 {
				t.Fatalf("the shown pictures leave a gap of %v and repeat %d times across the cut, want none missing and at most the one cut short made again", gap, repeats)
			}
			if forward, back := steps(sounds); forward > 300*time.Millisecond || back > 0 {
				t.Fatalf("sound steps forward up to %v and back up to %v across the cut, want it continuous", forward, back)
			}
			if span := shown[len(shown)-1] - shown[0]; span < 14*time.Second {
				t.Fatalf("the device saw %v of a 16 s movie", span)
			}
		})
	}
}

func TestASoftwareEncoderThatHangsIsRecoveredNotLeftStuck(t *testing.T) {
	ffmpegPath, _ := requireFFmpegTools(t)
	shorterHangWindows(t)
	hung := failingHardware("sendcmd=c='8.0 realtime speed 0.000001'", "realtime=limit=100000")
	hung.Hardware = false
	began := time.Now()
	actions, retired, _, _ := castWith(t, &recordingDevice{caps: dlnaLike()}, chainFixture(t, ffmpegPath, 16), hung)
	if len(actions) == 0 || slices.Contains(actions, recovery.SoftwareEncode) || len(retired) > 0 {
		t.Fatalf("revisions %v, retired %v: want the hung software encoder recovered as a stall, not blamed on hardware", actions, retired)
	}
	if took := time.Since(began); took > time.Minute {
		t.Fatalf("the cast took %v to give up on a hung encoder", took)
	}
}

func TestAHardwareEncoderThatHangsOutsideAChainIsReplacedWhereTheViewerWas(t *testing.T) {
	ffmpegPath, _ := requireFFmpegTools(t)
	shorterHangWindows(t)
	// A renderer that takes only MP4 is served by one encoder over the buffer, not a chain.
	mp4 := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}}
	dev := &recordingDevice{caps: mp4}
	actions, retired, run, err := castWith(t, dev, chainFixture(t, ffmpegPath, 16), failingHardware("sendcmd=c='8.0 realtime speed 0.000001'", "realtime=limit=100000"))
	if err != nil {
		t.Fatalf("the cast failed instead of carrying on in software: %v", err)
	}
	if !slices.Equal(actions, []recovery.Action{recovery.SoftwareEncode}) || !slices.Equal(run.software, []bool{false, true}) {
		t.Fatalf("revisions %v with attempts in software %v, want one software-encode", actions, run.software)
	}
	if !slices.Equal(retired, []string{"libx264"}) {
		t.Fatalf("retired %v, want the hung hardware encoder retired", retired)
	}
}

func TestAnEncoderIsNotToldHungWhileCastorHoldsItBack(t *testing.T) {
	ffmpegPath, _ := requireFFmpegTools(t)
	shorterHangWindows(t)
	healthy := failingHardware()
	t.Run("the device takes the stream slowly", func(t *testing.T) {
		dev := &recordingDevice{caps: dlnaLike(), rate: 40 << 10}
		actions, retired, _, err := castWith(t, dev, chainFixture(t, ffmpegPath, 8), healthy)
		if err != nil || len(actions) != 0 || len(retired) != 0 {
			t.Fatalf("err %v, revisions %v, retired %v: a device that reads slowly made a healthy encoder look hung", err, actions, retired)
		}
	})
	t.Run("the source arrives slowly", func(t *testing.T) {
		file := generateFixture(t, ffmpegPath, "movie.ts", 8, []string{"-c:v", "mpeg4", "-g", "15", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.ServeContent(w, r, "movie.ts", time.Time{}, slowFile(t, file))
		}))
		t.Cleanup(srv.Close)
		u, _ := url.Parse(srv.URL + "/movie.ts")
		actions, retired, _, _ := castWith(t, &recordingDevice{caps: dlnaLike()}, &source.Stream{URL: u, ContentType: media.MPEGTS}, healthy)
		if slices.Contains(actions, recovery.SoftwareEncode) || len(retired) != 0 {
			t.Fatalf("revisions %v, retired %v: an encoder waiting on a slow source was told hung", actions, retired)
		}
	})
}

func TestHangAndFailureUnits(t *testing.T) {
	t.Run("silence counts only with input waiting", func(t *testing.T) {
		if silent(time.Minute, hangMargin-1) != 0 || silent(time.Minute, hangMargin) != time.Minute {
			t.Fatal("an encoder was told silent without its input waiting, or not with it")
		}
	})
	t.Run("a hung encoder is the hardware's when it is hardware, a stall otherwise", func(t *testing.T) {
		hung := &encoderHung{waited: 10 * time.Second}
		for _, hardware := range []bool{true, false} {
			opts := encodePlan{Video: codec.Encode(codec.VideoEncode{
				Encoder: ffmpeg.Encoder{Name: "h264_mediacodec", Codec: media.CodecH264, Hardware: hardware},
			})}
			err := (&coordinator{}).encoderFailed(hung, opts, quiet{})
			_, blamed := errors.AsType[*hardwareFailed](err)
			fault, faulted := errors.AsType[*health.Fault](err)
			if blamed != hardware || !faulted || (!hardware && fault.Kind != health.Stalled) {
				t.Fatalf("hardware %v: got %v", hardware, err)
			}
		}
	})
}

// slowFile is a file read at about 20 KB a second, slower than the movie plays.
func slowFile(t *testing.T, path string) io.ReadSeeker {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return &trickle{data: data}
}

type trickle struct {
	data []byte
	at   int64
}

func (r *trickle) Read(p []byte) (int, error) {
	if r.at >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n := copy(p[:min(len(p), 4<<10)], r.data[r.at:])
	r.at += int64(n)
	time.Sleep(200 * time.Millisecond)
	return n, nil
}

func (r *trickle) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		r.at = offset
	case io.SeekCurrent:
		r.at += offset
	case io.SeekEnd:
		r.at = int64(len(r.data)) + offset
	}
	return r.at, nil
}
