package cast

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"connectrpc.com/connect"
	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestPagesWithoutScrapingFailWithoutCallingMedia(t *testing.T) {
	s := New(&mediav1.PlaybackSettings{}, nil, nil, nil)
	defer s.Drain(t.Context())
	_, err := s.Resolve(t.Context(), &castorv1.ResolveRequest{Source: &castorv1.Source{Source: &castorv1.Source_Pages_{Pages: &castorv1.Source_Pages{Urls: []string{"https://site.example/watch"}}}}})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("missing scraping resolver: %v, want unavailable (no media fallback)", err)
	}
}

type interruptedTarget struct{ cancel context.CancelFunc }

func (d interruptedTarget) Target(context.Context, *castorv1.Target) (device.Info, error) {
	d.cancel()
	return device.Info{Type: "dlna", Address: "10.0.0.9"}, nil
}

func (interruptedTarget) Connect(context.Context, device.Info) (device.Device, error) {
	return nil, errors.New("no device should connect after request cancellation")
}

func TestACastCancelledWhileFindingItsTargetDoesNotStart(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := New(&mediav1.PlaybackSettings{}, interruptedTarget{cancel: cancel}, nil, nil)
	defer s.Drain(t.Context())
	response, err := s.Cast(ctx, &castorv1.CastRequest{})
	if !errors.Is(err, context.Canceled) || response != nil {
		t.Fatalf("Cast = %v, %v; started an unacknowledged cast after the request was cancelled", response, err)
	}
}

type blockingTarget struct {
	interruptedTarget
	started chan struct{}
}

func (d blockingTarget) Target(ctx context.Context, _ *castorv1.Target) (device.Info, error) {
	close(d.started)
	<-ctx.Done()
	return device.Info{}, ctx.Err()
}

func TestShutdownCancelsTargetDiscoveryBeforeACastIsRegistered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		d := blockingTarget{started: make(chan struct{})}
		s := New(&mediav1.PlaybackSettings{}, d, nil, nil)
		defer s.Drain(t.Context())
		result := make(chan error, 1)
		go func() { _, err := s.Cast(ctx, &castorv1.CastRequest{}); result <- err }()
		<-d.started
		s.Drain(t.Context())
		synctest.Wait()
		select {
		case err := <-result:
			if connect.CodeOf(err) != connect.CodeUnavailable {
				t.Fatalf("Cast = %v, want unavailable when discovery was interrupted by shutdown", err)
			}
		default:
			cancel()
			<-result
			t.Fatal("shutdown left target discovery running before cast registration")
		}
	})
}

func TestListedCastsAndDefaultsDoNotExposeTheServicesSnapshots(t *testing.T) {
	defaults := &mediav1.PlaybackSettings{MaxHeight: 1080}
	s := New(defaults, nil, nil, nil)
	defer s.Drain(t.Context())
	defaults.MaxHeight = 2
	if got := s.asked(nil).GetMaxHeight(); got != 1080 {
		t.Errorf("changing the caller's defaults changed future casts to height %d", got)
	}
	c := newCast("cast-1", &castorv1.Device{Name: "Bedroom"}, &castorv1.CastRequest{Source: &castorv1.Source{Source: &castorv1.Source_Stream{Stream: &castorv1.Stream{Url: "https://media.example/video"}}}}, func(error) {})
	status := &castorv1.CastStatus{State: &castorv1.CastStatus_Casting{Casting: &castorv1.CastingStatus{Attempt: 1}}}
	c.show(status)
	done := make(chan struct{})
	close(done)
	s.casts.Add(c.id, c, done)
	listed, _ := s.ListCasts(t.Context(), &castorv1.ListCastsRequest{})
	listed.Casts[0].Device.Name = "mutated"
	listed.Casts[0].Status.State = &castorv1.CastStatus_Connecting{Connecting: &emptypb.Empty{}}
	status.GetCasting().Attempt = 99
	again, _ := s.ListCasts(t.Context(), &castorv1.ListCastsRequest{})
	if got := again.Casts[0]; got.Device.Name != "Bedroom" || got.Status.GetCasting().GetAttempt() != 1 {
		t.Fatalf("mutating a list or a published status changed the stored cast: %v", got)
	}
}

type busyDevice struct{ interruptedTarget }

func (busyDevice) Connect(context.Context, device.Info) (device.Device, error) {
	return nil, &device.Busy{Address: "192.0.2.1"}
}
func TestBusyReceiverReportsActionablePlaybackFailure(t *testing.T) {
	s := &Service{devices: busyDevice{}}
	ended := s.play(t.Context(), &cast{}, device.Info{}, nil)
	if ended.GetFailed().GetCode() != castorv1.FailureCode_FAILURE_CODE_PLAYBACK_FAILED || !strings.Contains(ended.GetFailed().GetMessage(), "stop its current cast") {
		t.Fatalf("ended=%v", ended)
	}
}

type cancelingResolver struct {
	started  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (r cancelingResolver) ResolvePages(ctx context.Context, _ []string) ([]*castorv1.StreamCandidate, error) {
	close(r.started)
	<-ctx.Done()
	close(r.canceled)
	<-r.release
	return nil, ctx.Err()
}

func TestDrainCancelsAndJoinsResolution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := cancelingResolver{make(chan struct{}), make(chan struct{}), make(chan struct{})}
		s := New(&mediav1.PlaybackSettings{}, nil, nil, r)
		resolved := make(chan error, 1)
		go func() {
			_, err := s.Resolve(t.Context(), &castorv1.ResolveRequest{Source: &castorv1.Source{Source: &castorv1.Source_Pages_{Pages: &castorv1.Source_Pages{Urls: []string{"https://example.test"}}}}})
			resolved <- err
		}()
		<-r.started
		drained := make(chan struct{})
		go func() { s.Drain(t.Context()); close(drained) }()
		<-r.canceled
		synctest.Wait()
		select {
		case <-drained:
			t.Fatal("drain returned before resolver cleanup")
		default:
		}
		close(r.release)
		<-drained
		if err := <-resolved; !errors.Is(err, context.Canceled) {
			t.Fatalf("resolution = %v", err)
		}
		if _, err := s.Resolve(t.Context(), &castorv1.ResolveRequest{}); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("resolution accepted after shutdown: %v", err)
		}
	})
}

type cleaningTarget struct {
	interruptedTarget
	started  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (d cleaningTarget) Target(ctx context.Context, _ *castorv1.Target) (device.Info, error) {
	close(d.started)
	<-ctx.Done()
	close(d.canceled)
	<-d.release
	return device.Info{}, ctx.Err()
}

func TestDrainJoinsTargetDiscoveryCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := cleaningTarget{started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
		s := New(&mediav1.PlaybackSettings{}, d, nil, nil)
		result := make(chan error, 1)
		go func() { _, err := s.Cast(t.Context(), &castorv1.CastRequest{}); result <- err }()
		<-d.started
		drained := make(chan struct{})
		go func() { s.Drain(t.Context()); close(drained) }()
		<-d.canceled
		synctest.Wait()
		select {
		case <-drained:
			t.Error("drain returned while target discovery was cleaning up")
		default:
		}
		close(d.release)
		<-drained
		if err := <-result; connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("cast = %v", err)
		}
	})
}
