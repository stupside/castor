package execute

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/progressive"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func awaitProduction(t *testing.T, p *production) {
	t.Helper()
	select {
	case <-p.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("production did not join")
	}
}

func TestProductionDoneIncludesProcessResult(t *testing.T) {
	input, send := io.Pipe()
	defer input.Close()
	defer send.Close()
	proc, err := ffmpeg.Start(t.Context(), "/bin/sh", ffmpeg.Command{Args: []string{"-c", "exec 1>&-; read line; exit 7"}}, ffmpeg.Options{Stdin: input})
	if err != nil {
		t.Fatal(err)
	}
	packaged := make(chan struct{})
	p := startProduction(t.Context(), proc, nil, func(_ context.Context, r io.Reader) error {
		_, err := io.Copy(io.Discard, r)
		close(packaged)
		return err
	})
	t.Cleanup(func() { _ = input.Close(); _ = p.Close() })
	<-packaged
	select {
	case <-p.Done():
		t.Fatal("stdout EOF published completion before the process exited")
	default:
	}
	_ = send.Close()
	awaitProduction(t, p)
	if p.Err() == nil || !strings.Contains(p.Err().Error(), "exit status 7") {
		t.Fatalf("final result = %v", p.Err())
	}
}

func TestProductionCancellationInterruptsAndJoinsPackager(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	proc, err := ffmpeg.Start(ctx, "/bin/sh", ffmpeg.Command{Args: []string{"-c", "exec sleep 30"}}, ffmpeg.Options{})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	failure := errors.New("packager failure")
	p := startProduction(ctx, proc, nil, func(_ context.Context, r io.Reader) error {
		close(entered)
		_, err := io.Copy(io.Discard, r)
		return errors.Join(err, context.Canceled, failure)
	})
	t.Cleanup(func() { _ = p.Close() })
	<-entered
	cancel()
	awaitProduction(t, p)
	if !errors.Is(p.Err(), failure) {
		t.Fatalf("lost unrelated packaging failure: %v", p.Err())
	}
	if errors.Is(p.Err(), context.Canceled) {
		t.Fatalf("the stop became a packaging failure: %v", p.Err())
	}
}

func TestPackagerFailureStopsEncoder(t *testing.T) {
	proc, err := ffmpeg.Start(t.Context(), "/bin/sh", ffmpeg.Command{Args: []string{"-c", "exec sleep 30"}}, ffmpeg.Options{})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("cannot store output")
	p := startProduction(t.Context(), proc, nil, func(context.Context, io.Reader) error { return failure })
	t.Cleanup(func() { _ = p.Close() })
	awaitProduction(t, p)
	if !errors.Is(p.Err(), failure) {
		t.Fatalf("result = %v", p.Err())
	}
}

func TestProductionCompletionKeepsPublicationAndStorageGuard(t *testing.T) {
	failures := make(chan error)
	s := testPreparation(t.Context(), Cast{}, recovery.Attempt{})
	defer func() { _ = s.releases.release() }()
	format, _ := container.For(media.MP4)
	stream, err := progressive.NewStream(output.Opening{Format: format}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.releases.push(stream.Close)
	pub, err := (loopback{}).Publish(s.ctx, stream.Handler())
	if err != nil {
		t.Fatal(err)
	}
	s.releases.publication(pub.Close)
	proc, err := ffmpeg.Start(s.ctx, "/bin/sh", ffmpeg.Command{Args: []string{"-c", "printf artifact"}}, ffmpeg.Options{})
	if err != nil {
		t.Fatal(err)
	}
	p := startProduction(s.ctx, proc, nil, stream.Write)
	s.releases.worker(p.stop, func() error { <-p.Done(); return p.Err() })
	failStorage := make(chan struct{})
	s.failures = failures
	failure := errors.New("storage limit")
	s.watchStorage(func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return nil
		case <-failStorage:
			return failure
		}
	})
	awaitProduction(t, p)
	if p.Err() != nil {
		t.Fatal(p.Err())
	}
	resp, err := http.Get(pub.URL(stream.Path()).String())
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(body) != "artifact" {
		t.Fatalf("published artifact = %q, %v", body, err)
	}
	close(failStorage)
	select {
	case got := <-failures:
		if !errors.Is(got, failure) {
			t.Fatalf("storage report = %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("storage monitor ended with production")
	}
	if s.ctx.Err() != nil {
		t.Fatal("preparation canceled the attempt instead of reporting its failure")
	}
}

func TestStorageFailureDuringTeardownIsRetained(t *testing.T) {
	s := testPreparation(t.Context(), Cast{}, recovery.Attempt{})
	failure := errors.New("final storage check failed")
	s.watchStorage(func(ctx context.Context) error {
		<-ctx.Done()
		return failure
	})
	s.releases.signal()
	if err := s.releases.release(); !errors.Is(err, failure) {
		t.Fatalf("cleanup lost storage failure: %v", err)
	}
}

func TestCancellationPreservesExitedEncoderFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	proc, err := ffmpeg.Start(ctx, "/bin/sh", ffmpeg.Command{Args: []string{"-c", "exit 7"}}, ffmpeg.Options{})
	if err != nil {
		t.Fatal(err)
	}
	drained := make(chan struct{})
	p := startProduction(ctx, proc, nil, func(ctx context.Context, r io.Reader) error {
		_, err := io.Copy(io.Discard, r)
		close(drained)
		<-ctx.Done()
		return err
	})
	t.Cleanup(func() { _ = p.Close() })
	<-drained
	cancel()
	awaitProduction(t, p)
	if p.Err() == nil || !strings.Contains(p.Err().Error(), "exit status 7") {
		t.Fatalf("cancellation hid process failure: %v", p.Err())
	}
}

type refusingPublisher struct{ err error }

func (p refusingPublisher) Publish(context.Context, http.Handler) (output.Publication, error) {
	return nil, p.err
}

func TestPublicationFailureReleasesPreparedOutputBeforeEncoderStart(t *testing.T) {
	refused := errors.New("publication unavailable")
	s := servingAttempt(t, "/nonexistent/castor-test-encoder")
	s.capabilities = observedDevice{}.Capabilities()
	s.Publisher = refusingPublisher{err: refused}
	opts := testEncoding(t, verbatim())
	work := t.TempDir()
	_, err := s.produce(work, feed{}, opts, nil)
	if !errors.Is(err, refused) {
		t.Fatalf("expected publication failure before encoder start: %v", err)
	}
	artifact := filepath.Join(work, "delivery", "out"+opts.Format.Extension)
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("output was not prepared: %v", err)
	}
	if err := s.releases.release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(artifact); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prepared output leaked after publication failure: %v", err)
	}
}
