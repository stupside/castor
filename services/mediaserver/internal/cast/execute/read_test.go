package execute

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

// fakeTranscript is a burn-in without whisper; production needs a transcription model and cgo.
type fakeTranscript struct {
	cues subtitle.Cues
	lead *fakeLead

	// linger is how long Run keeps going once its feed ends, as a transcription flushing its last cues does.
	linger time.Duration

	attached atomic.Int64
	ended    atomic.Int64
	drained  atomic.Int64
	leadAsks atomic.Int64
}

func (b *fakeTranscript) Run(_ context.Context, pcm io.ReadCloser) {
	b.attached.Add(1)
	defer b.ended.Add(1)
	if pcm == nil {
		return
	}
	defer pcm.Close()
	n, _ := io.Copy(io.Discard, pcm)
	b.drained.Add(n)
	time.Sleep(b.linger)
}

// SampleRate is whisper's, so the read tees what a real transcription asks for.
func (b *fakeTranscript) SampleRate() int { return 16000 }

func (b *fakeTranscript) Cues() *subtitle.Cues { return &b.cues }

func (b *fakeTranscript) LatestEnd() float64 {
	b.leadAsks.Add(1)
	if b.lead == nil {
		return 0
	}
	return b.lead.latest
}

func (b *fakeTranscript) Done() bool { return b.lead == nil || b.lead.done }

// subtitling burns in b, whatever the cast reads.
func subtitling(b Transcript) func(context.Context, string) Subtitles {
	return func(context.Context, string) Subtitles { return func(context.Context) Transcript { return b } }
}

// TestASilentSourceRunsNoBurnInAtAll: a PCM tee of a source with no audio maps nothing and ffmpeg refuses.
func TestASilentSourceRunsNoBurnInAtAll(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveSilentFixture(t, ffmpegPath)

	program := programFromStream(t, origin.stream())
	c := realCast(&fakeDevice{}, ffmpegPath, ffprobePath)
	c.Transcription = subtitling(&fakeTranscript{})
	s := readingAttempt(t, c, recovery.Attempt{Program: program, Fetch: sourceFetchPlan(t, program, testReadDeadline)})

	followed, err := s.follow(program)
	if err != nil {
		t.Fatal(err)
	}
	buf, err := s.read(t.TempDir(), followed)
	if err != nil {
		t.Fatalf("starting the read: %v", err)
	}
	<-buf.reader.Done()
	if err := buf.reader.Err(); err != nil {
		t.Fatalf("the read of a silent source failed: %v\n%q", err, buf.reader.Evidence())
	}
	if buf.transcript != nil || buf.reader.pcm != nil {
		t.Error("a silent source was given a burn-in or a PCM tee")
	}
	if buf.reader.spool.Size() == 0 {
		t.Error("the buffer is empty, so nothing was read at all")
	}
}

// The transcription writes into the work dir, so releasing its read stops it before the dir goes.
func TestReleasingAReadStopsItsTranscriptionBeforeTheWorkDirGoes(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveFixture(t, ffmpegPath)

	burn := &fakeTranscript{linger: 200 * time.Millisecond}
	c := realCast(&fakeDevice{}, ffmpegPath, ffprobePath)
	c.Transcription = subtitling(burn)
	program := programFromStream(t, origin.stream())
	s := readingAttempt(t, c, recovery.Attempt{Program: program, Fetch: sourceFetchPlan(t, program, testReadDeadline)})

	// Stands for the workspace, acquired before the read and so released after it.
	var running int64
	s.releases.push(func() error { running = burn.attached.Load() - burn.ended.Load(); return nil })
	followed, err := s.follow(program)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.read(t.TempDir(), followed); err != nil {
		t.Fatalf("starting the read: %v", err)
	}
	if err := s.releases.release(); err != nil {
		t.Fatal(err)
	}
	if burn.attached.Load() != 1 || running != 0 {
		t.Errorf("the work dir went with %d transcription(s) of %d still running", running, burn.attached.Load())
	}
}

// A read released while its source still answers is stopped, not waited out to the source's end or deadline.
func TestReleasingAReadStopsItWhileItsSourceStillAnswers(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	stream := &source.Stream{URL: quietOrigin(t, programHead(t, ffmpegPath)), ContentType: media.MPEGTS}
	program := programFromStream(t, stream)
	s := readingAttempt(t, realCast(&fakeDevice{}, ffmpegPath, ffprobePath), recovery.Attempt{Program: program, Fetch: sourceFetchPlan(t, program, time.Hour)})

	buf, err := s.read(t.TempDir(), program)
	if err != nil {
		t.Fatalf("starting the read: %v", err)
	}
	released := make(chan error, 1)
	go func() { released <- s.releases.release() }()
	select {
	case <-released:
	case <-time.After(10 * time.Second):
		t.Fatal("releasing a read of a source that keeps answering waited for the source instead of stopping the read")
	}
	if _, running := <-buf.reader.Done(); running {
		t.Error("the read was still running once released")
	}
}

// TestABurnInIsFedAndAwaitedByTheCastThatRunsIt drives a whole cast with a burn.
func TestABurnInIsFedAndAwaitedByTheCastThatRunsIt(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveFixture(t, ffmpegPath)

	burn := &fakeTranscript{lead: &fakeLead{done: true}}
	dev := &fakeDevice{caps: dlnaLike(), drain: true}

	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	program := programFromStream(t, origin.stream())
	c := realCast(dev, ffmpegPath, ffprobePath)
	c.Transcription = subtitling(burn)
	out := c.Run(ctx, recovery.Attempt{Try: 1, Program: program, Fetch: sourceFetchPlan(t, program, testReadDeadline)})
	if out.Err != nil {
		t.Fatalf("casting with a burn: %v", out.Err)
	}

	if got := burn.attached.Load(); got != 1 {
		t.Errorf("the burn was started %d times, want exactly once", got)
	}
	if burn.drained.Load() == 0 {
		t.Error("the burn was handed no audio")
	}
	if burn.leadAsks.Load() == 0 {
		t.Error("nothing asked the burn how far it had committed, so the gate is not holding for it")
	}
}

// readingAttempt is an attempt's session, released when the test ends.
func readingAttempt(t *testing.T, c Cast, a recovery.Attempt) *preparation {
	t.Helper()
	s := testPreparation(t.Context(), c, a)
	t.Cleanup(func() { _ = s.releases.release() })
	return s
}

// A consumer may stop reading before it finishes its own shutdown. The read
// must interrupt its writer itself before waiting for FFmpeg's PCM copy.
type stalledTranscript struct {
	fakeTranscript
	started chan struct{}
	finish  chan struct{}
}

func (b *stalledTranscript) Run(ctx context.Context, pcm io.ReadCloser) {
	defer pcm.Close()
	var first [1]byte
	_, _ = pcm.Read(first[:])
	close(b.started)
	<-ctx.Done()
	// Model transcription still finishing inference after cancellation.
	<-b.finish
}

func TestReadOwnsPCMInterruption(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveFixture(t, ffmpegPath)
	transcript := &stalledTranscript{started: make(chan struct{}), finish: make(chan struct{})}
	c := realCast(&fakeDevice{}, ffmpegPath, ffprobePath)
	c.Transcription = func(context.Context, string) Subtitles {
		return func(context.Context) Transcript { return transcript }
	}
	program := programFromStream(t, origin.stream())
	s := readingAttempt(t, c, recovery.Attempt{Program: program, Fetch: sourceFetchPlan(t, program, testReadDeadline)})
	buf, err := s.read(t.TempDir(), program)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-transcript.started:
	case <-time.After(5 * time.Second):
		t.Fatal("PCM did not start")
	}
	released := make(chan error, 1)
	go func() { released <- s.releases.release() }()
	select {
	case <-buf.reader.Done():
	case <-time.After(3 * time.Second):
		close(transcript.finish)
		t.Fatal("PCM writer waited for transcription shutdown")
	}
	close(transcript.finish)
	select {
	case err := <-released:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("read waited for its PCM consumer to close the pipe")
	}
}

// testPreparation supplies the attempt-owned scope without a playback coordinator.
func testPreparation(parent context.Context, c Cast, a recovery.Attempt) *preparation {
	ctx, cancel := context.WithCancelCause(parent)
	scope := &releases{ctx: ctx}
	scope.interrupt(func() { cancel(nil) })
	return newPreparation(ctx, c, a, scope, make(chan error, 1))
}
