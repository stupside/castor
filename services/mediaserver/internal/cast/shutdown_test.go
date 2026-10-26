package cast

import (
	"context"
	"errors"
	"github.com/stupside/castor/services/mediaserver/internal/cast/execute"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/connect"
	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

type drainingResolver struct {
	retrying
	entered chan struct{}
	release chan struct{}
}

func (r drainingResolver) Measure(ctx context.Context, _ *source.Stream) (*source.Stream, error) {
	close(r.entered)
	<-ctx.Done()
	<-r.release
	return nil, context.Cause(ctx)
}

func TestDrainCancelsAndJoinsResolutionRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := drainingResolver{entered: make(chan struct{}), release: make(chan struct{})}
		s := New(func(*mediav1.PlaybackSettings) Caster { return r }, nil)
		req := &mediav1.RankRequest{Source: &mediav1.Source{Source: &mediav1.Source_Stream{Stream: &castorv1.Stream{Url: "https://example.com/video"}}}}
		result := make(chan error, 1)
		go func() { _, err := s.Rank(t.Context(), req); result <- err }()
		<-r.entered
		drained := make(chan struct{})
		go func() { s.Drain(t.Context()); close(drained) }()
		synctest.Wait()
		select {
		case <-drained:
			t.Fatal("shutdown returned before resolution cleanup")
		default:
		}
		if _, err := s.Rank(t.Context(), req); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("new resolution during shutdown: %v", err)
		}
		close(r.release)
		<-drained
		if err := <-result; connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("interrupted resolution: %v", err)
		}
	})
}

type recoveringCaster struct {
	retrying
	resolving   chan struct{}
	release     chan struct{}
	resolutions int
	attempts    int
}

func (r *recoveringCaster) Play(ctx context.Context, _ execute.Device, _ output.Publisher, streams []*source.Stream, _ time.Duration, turns recovery.Turns) error {
	backup := *streams[0]
	u := *backup.URL
	u.Path = "/backup.mp4"
	backup.URL = &u
	return recovery.Cast(ctx, recovery.Intent{Candidates: []*source.Stream{streams[0], &backup}, Turns: turns}, r, r)
}

func (r *recoveringCaster) Resolve(ctx context.Context, s *source.Stream, _ source.Rendition, _ media.Bitrate) (source.Resolution, error) {
	r.resolutions++
	if r.resolutions == 2 {
		close(r.resolving)
		<-ctx.Done()
		<-r.release
	}
	// A resolver may finish successfully while cancellation is arriving.
	program, err := source.ProgramFor(s)
	return source.Resolution{Program: program}, err
}

func (r *recoveringCaster) Run(context.Context, recovery.Attempt) recovery.Outcome {
	r.attempts++
	err := errors.New("unreadable source timeline")
	return recovery.Outcome{Err: err, Evidence: recovery.Evidence{TimelineErr: err}}
}

func TestDrainJoinsRecoveryResolutionBeforePublishingCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recoveringCaster{resolving: make(chan struct{}), release: make(chan struct{})}
		s := New(func(*mediav1.PlaybackSettings) Caster { return r }, nil)
		unblock := sync.OnceFunc(func() { close(r.release) })
		defer unblock()
		_, err := s.Start(t.Context(), &mediav1.StartRequest{CastId: "recovering", Source: &mediav1.Source{Source: &mediav1.Source_Stream{Stream: &castorv1.Stream{Url: "https://example.com/first.mp4"}}}})
		if err != nil {
			t.Fatal(err)
		}
		c, err := s.find("recovering")
		if err != nil {
			t.Fatal(err)
		}
		if err := c.lend(media.Capabilities{}); err != nil {
			t.Fatal(err)
		}
		<-r.resolving
		drained := make(chan struct{})
		go func() { s.Drain(t.Context()); close(drained) }()
		synctest.Wait()
		select {
		case <-drained:
			t.Fatal("drain returned while recovery still owned resolution work")
		default:
		}
		if v, _ := c.now.Load(); v.ended != nil {
			t.Fatal("terminal status published before recovery joined")
		}
		unblock()
		<-drained
		if r.resolutions != 2 || r.attempts != 1 {
			t.Fatalf("work started after shutdown: resolutions=%d attempts=%d", r.resolutions, r.attempts)
		}
		v, _ := c.now.Load()
		if v.ended.GetFailed().GetCode() != castorv1.FailureCode_FAILURE_CODE_SERVER_SHUTDOWN {
			t.Fatalf("shutdown outcome %v", v.ended)
		}
		if _, err := s.Start(t.Context(), &mediav1.StartRequest{CastId: "too-late"}); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("cast admitted after shutdown: %v", err)
		}
	})
}
