package execute

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

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

// carriedCast is how a cast went: the revisions it made, and each attempt's software choice.
type carriedCast struct {
	actions  []recovery.Action
	software []bool
	err      error
}

// castWith casts stream to dev, carrying chains on, with encoder offered as the hardware one.
func castWith(t *testing.T, dev Device, stream *source.Stream, encoder ffmpeg.Encoder) carriedCast {
	t.Helper()
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	c := realCast(dev, ffmpegPath, ffprobePath)
	software := ffmpeg.Encoders(ffmpegPath, nil)
	c.Encoders = func(ctx context.Context, codec media.Codec, soft bool) (ffmpeg.Encoder, bool) {
		if soft || codec != media.CodecH264 {
			return software(ctx, codec, true)
		}
		return encoder, true
	}
	c.CarryOn = true
	var mu sync.Mutex
	var out carriedCast
	run := runnerFunc(func(ctx context.Context, a recovery.Attempt) recovery.Outcome {
		mu.Lock()
		out.software = append(out.software, a.Software)
		mu.Unlock()
		return c.Run(ctx, a)
	})
	turns := &revisions{}
	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	out.err = recovery.Cast(ctx, recovery.Intent{Candidates: []*source.Stream{stream}, Deadline: testReadDeadline, Turns: turns}, run, linkOrigin{t})
	out.actions = turns.actions
	return out
}

// shorterHangWindow lets the tests tell a hung encoder within seconds.
func shorterHangWindow(t *testing.T) {
	window := chainHangWindow
	chainHangWindow = 2 * time.Second
	t.Cleanup(func() { chainHangWindow = window })
}

func TestAHardwareEncoderThatFailsInAChainIsCarriedOnInSoftwareInTheSameStream(t *testing.T) {
	ffmpegPath, _ := requireFFmpegTools(t)
	shorterHangWindow(t)
	for _, tt := range []struct {
		name    string
		encoder ffmpeg.Encoder
	}{
		{name: "it dies", encoder: failingHardware("scale=w='if(gt(t,8),160,320)':h=-2:eval=frame")},
		{name: "it hangs", encoder: failingHardware("sendcmd=c='8.0 realtime speed 0.000001'", "realtime=limit=100000")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dev := &recordingDevice{caps: dlnaLike()}
			cast := castWith(t, dev, chainFixture(t, ffmpegPath, 16), tt.encoder)
			if cast.err != nil {
				t.Fatalf("the cast failed instead of carrying on: %v", cast.err)
			}
			if len(cast.actions) != 0 || dev.loads != 1 {
				t.Fatalf("revisions %v over %d streams, want the delivery carried on in the one stream", cast.actions, dev.loads)
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

func TestAnEncoderIsNotToldHungWhileCastorHoldsItBack(t *testing.T) {
	ffmpegPath, _ := requireFFmpegTools(t)
	shorterHangWindow(t)
	healthy := failingHardware()
	t.Run("the device takes the stream slowly", func(t *testing.T) {
		cast := castWith(t, &recordingDevice{caps: dlnaLike(), rate: 40 << 10}, chainFixture(t, ffmpegPath, 8), healthy)
		if cast.err != nil || len(cast.actions) != 0 {
			t.Fatalf("err %v, revisions %v: a device that reads slowly made a healthy encoder look hung", cast.err, cast.actions)
		}
	})
	t.Run("the source arrives slowly", func(t *testing.T) {
		file := generateFixture(t, ffmpegPath, "movie.ts", 8, []string{"-c:v", "mpeg4", "-g", "15", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.ServeContent(w, r, "movie.ts", time.Time{}, slowFile(t, file))
		}))
		t.Cleanup(srv.Close)
		u, _ := url.Parse(srv.URL + "/movie.ts")
		cast := castWith(t, &recordingDevice{caps: dlnaLike()}, &source.Stream{URL: u, ContentType: media.MPEGTS}, healthy)
		if slices.Contains(cast.actions, recovery.SoftwareEncode) {
			t.Fatalf("revisions %v: an encoder waiting on a slow source was told hung", cast.actions)
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
