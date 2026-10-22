package execute

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"

	"golang.org/x/sync/semaphore"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/probe"
)

// TestTheReadsFloorEncodeIsCappedAtTheCastsCeiling: an uncapped read encode falls below realtime.
func TestTheReadsFloorEncodeIsCappedAtTheCastsCeiling(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveFixture(t, ffmpegPath)

	// The fixture is 240p; the ceiling sits under it.
	c := realCast(&fakeDevice{}, ffmpegPath, ffprobePath)
	c.MaxHeight = 120
	program := programFromStream(t, origin.stream())

	s := readingAttempt(t, c, recovery.Attempt{Program: program, Fetch: sourceFetchPlan(t, program, testReadDeadline), Decode: media.Axes{Video: true}})
	followed, err := s.follow(program)
	if err != nil {
		t.Fatal(err)
	}
	buf, err := s.read(t.TempDir(), followed)
	if err != nil {
		t.Fatal(err)
	}
	<-buf.reader.Done()
	if err := buf.reader.Err(); err != nil {
		t.Fatalf("the read failed: %v\n%q", err, buf.reader.Evidence())
	}

	info, _, err := probe.FFprobe(ffprobePath).File(buf.reader.spool.Path()).Probe(t.Context())
	if err != nil {
		t.Fatalf("probing the buffer this read wrote: %v", err)
	}
	if info.VideoHeight != 120 {
		t.Errorf("the read produced a %dp picture, want the 120p ceiling", info.VideoHeight)
	}
}

// A spool that stops taking writes ends the read at once instead of leaving its ffmpeg blocked on a full pipe.
func TestAFailedSpoolWriteKillsTheReader(t *testing.T) {
	sp, err := storage.NewSpool(filepath.Join(t.TempDir(), "spool.ts"))
	if err != nil {
		t.Fatal(err)
	}
	sp.CloseWrite(nil)

	// yes writes to its stdout until it is killed.
	proc, err := ffmpeg.Start(t.Context(), "yes", ffmpeg.Command{}, ffmpeg.Options{})
	if err != nil {
		t.Fatal(err)
	}
	p := &pull{proc: proc, spool: sp, done: make(chan struct{})}
	go p.run(t.Context())

	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		proc.Kill()
		t.Fatal("the read never ended after its spool refused a write")
	}
	if p.Err() == nil {
		t.Error("the read reported success after its spool refused a write")
	}
}

func TestAStoppedReadIsTruncatedButNotFailed(t *testing.T) {
	sp, err := storage.NewSpool(filepath.Join(t.TempDir(), "spool.ts"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	stopped := errors.New("cast stopped")
	proc, err := ffmpeg.Start(ctx, "yes", ffmpeg.Command{}, ffmpeg.Options{})
	if err != nil {
		t.Fatal(err)
	}
	p := &pull{proc: proc, spool: sp, done: make(chan struct{})}
	go p.run(ctx)
	cancel(stopped)
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		proc.Kill()
		t.Fatal("the read never ended after it was stopped")
	}
	if err := p.Err(); err != nil {
		t.Errorf("a stopped read reported a failure: %v", err)
	}
	if !errors.Is(sp.Err(), context.Canceled) {
		t.Errorf("a stopped read left its spool %v, want it truncated", sp.Err())
	}
}

func TestSpoolExhaustionSurvivesKilledFFmpegPipe(t *testing.T) {
	sp, err := storage.NewSpoolWithLimit(filepath.Join(t.TempDir(), "limited.ts"), 4)
	if err != nil {
		t.Fatal(err)
	}
	proc, err := ffmpeg.Start(t.Context(), "yes", ffmpeg.Command{}, ffmpeg.Options{})
	if err != nil {
		t.Fatal(err)
	}
	p := &pull{proc: proc, spool: sp, done: make(chan struct{})}
	go p.run(t.Context())
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		proc.Kill()
		t.Fatal("writer did not stop")
	}
	if !errors.Is(p.Err(), storage.ErrSpoolLimit) {
		t.Fatalf("lost budget error: %v", p.Err())
	}
	e := evidence(ran{reader: p}, p.Err(), false)
	if !errors.Is(e.ResourceErr, storage.ErrSpoolLimit) {
		t.Fatalf("lost budget evidence: %+v", e)
	}
}

func TestEncoderResultPreservesExpiredLiveInput(t *testing.T) {
	sp, err := storage.NewLiveSpoolWithBudget(filepath.Join(t.TempDir(), "live.ts"), 8, semaphore.NewWeighted(16))
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	tail, err := sp.TailAt(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tail.Close()
	if _, err := sp.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	proc, err := ffmpeg.Start(t.Context(), "sh", ffmpeg.Command{Args: []string{"-c", "cat >/dev/null; exit 7"}}, ffmpeg.Options{Stdin: tail})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, proc.Stdout)
	err = encoderResult(t.Context(), proc)
	if !errors.Is(err, storage.ErrSpoolExpired) {
		t.Fatalf("encoder erased expiry: %v", err)
	}
	if got := evidence(ran{}, err, false); !errors.Is(got.ResourceErr, storage.ErrSpoolExpired) {
		t.Fatalf("lost expiry evidence: %+v", got)
	}
}
