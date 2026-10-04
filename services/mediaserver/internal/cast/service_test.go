package cast

import (
	"context"
	"net/url"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
	"github.com/stupside/castor/services/mediaserver/internal/cast/execute"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

func TestACastNobodyLendsADeviceIsAbandonedAfterItsGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := New(func(*mediav1.PlaybackSettings) Caster { return nil }, &url.URL{Scheme: "http", Host: "127.0.0.1:8410"})
		started, err := svc.Start(t.Context(), &mediav1.StartRequest{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := svc.find(started.GetCastId())
		if err != nil {
			t.Fatal(err)
		}

		synctest.Sleep(undriven - time.Nanosecond)
		if now, _ := s.now.Load(); now.ended != nil {
			t.Fatal("abandoned before its grace ran out")
		}
		synctest.Sleep(time.Nanosecond)
		if now, _ := s.now.Load(); now.ended.GetFailed().GetCode() != castorv1.FailureCode_FAILURE_CODE_DEVICE_UNREACHABLE || now.ended.GetFailed().GetMessage() != errUndriven.Error() {
			t.Errorf("ended %v, want failed for want of a device", now.ended)
		}
	})
}

// retrying tries twice, revises once, then plays until the cast is stopped.
type retrying struct{}

func (retrying) Rank(_ context.Context, streams []*source.Stream) ([]*source.Stream, error) {
	return streams, nil
}

func (retrying) Measure(_ context.Context, stream *source.Stream) (*source.Stream, error) {
	return stream, nil
}

func (retrying) Play(ctx context.Context, _ execute.Device, _ deliver.Listeners, _ []*source.Stream, turns recovery.Turns) error {
	turns.Attempting(1)
	turns.Attempting(2)
	turns.Revising(recovery.RelaxRead, "stalled")
	<-ctx.Done()
	return ctx.Err()
}

func TestALentCastOutlivesItsGraceAndShowsNothingAfterItsEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := &mediav1.Source{Source: &mediav1.Source_Stream{Stream: &castorv1.Stream{Url: "https://cdn.example/direct"}}}
		s := newCast(t.Context(), "cast", &url.URL{Scheme: "http", Host: "127.0.0.1:8410"}, retrying{}, stream)
		if err := s.lend(media.Capabilities{}); err != nil {
			t.Fatal(err)
		}

		synctest.Sleep(undriven)
		now, _ := s.now.Load()
		if now.ended != nil {
			t.Fatal("a lent cast was abandoned when its grace ran out")
		}
		if casting := now.status.GetCasting(); casting.GetAttempt() != 2 || casting.GetRevision().GetAction() != castorv1.RecoveryAction_RECOVERY_ACTION_RELAX_READ || casting.GetStreams() != 1 || casting.GetCastable() != 1 {
			t.Errorf("shown %v, want the second attempt and its revision", now.status)
		}

		s.cancel(errStopped)
		<-s.done
		s.Revising(recovery.DecodeAxis, "too late")
		if now, _ := s.now.Load(); now.ended.GetStopped() == nil || now.status.GetCasting().GetRevision().GetAction() != castorv1.RecoveryAction_RECOVERY_ACTION_RELAX_READ {
			t.Errorf("after its end the cast shows %+v, want its outcome and nothing more", now)
		}
		if connect.CodeOf(s.lend(media.Capabilities{})) != connect.CodeFailedPrecondition {
			t.Error("a cast that is over took a device")
		}
	})
}

func TestMeasurementCountsSurviveAttemptsAndRevisions(t *testing.T) {
	s := newCast(t.Context(), "cast", &url.URL{}, nil, nil)
	now, _ := s.now.Load()
	if measuring := now.status.GetMeasuring(); measuring == nil || measuring.Castable != nil {
		t.Fatalf("initial status %v, want measuring before ranking", now.status)
	}
	s.fire(eventLendStream, func(*view) {})
	s.fire(eventMeasure, func(next *view) { next.status.GetMeasuring().Streams = 5 })
	s.fire(eventRank, func(next *view) { next.status.GetMeasuring().Castable = proto.Uint32(3) })
	measured, _ := s.now.Load()
	s.Attempting(1)
	first, _ := s.now.Load()
	if got := first.status.GetCasting(); got.GetStreams() != 5 || got.GetCastable() != 3 || got.GetAttempt() != 1 || got.GetRevision() != nil {
		t.Fatalf("first attempt %v, want retained counts and no revision", first.status)
	}
	s.Revising(recovery.SwitchCandidate, "refused")
	s.Attempting(2)
	now, _ = s.now.Load()
	casting := now.status.GetCasting()
	if casting.GetStreams() != 5 || casting.GetCastable() != 3 || casting.GetAttempt() != 2 || casting.GetRevision().GetAction() != castorv1.RecoveryAction_RECOVERY_ACTION_SWITCH_CANDIDATE || casting.GetRevision().GetWhy() != "refused" {
		t.Errorf("after switching candidate: %v", now.status)
	}
	if measured.status.GetMeasuring().GetStreams() != 5 || measured.status.GetMeasuring().GetCastable() != 3 || first.status.GetCasting().GetRevision() != nil {
		t.Error("later transitions changed an earlier snapshot")
	}
	s.end(eventEnd, nil)
	now, _ = s.now.Load()
	if now.ended.GetCompleted() == nil {
		t.Errorf("ended %v, want completed", now.ended)
	}
}
