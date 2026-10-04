package cast

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

type said struct{ msgs *[]string }

func (s said) Enabled(context.Context, slog.Level) bool { return true }
func (s said) Handle(_ context.Context, r slog.Record) error {
	*s.msgs = append(*s.msgs, r.Message)
	return nil
}
func (s said) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s said) WithGroup(string) slog.Handler      { return s }

func TestEachStateACastEntersIsAnnouncedOnce(t *testing.T) {
	var msgs []string
	prev := slog.Default()
	slog.SetDefault(slog.New(said{msgs: &msgs}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	var last *castorv1.CastStatus
	for _, now := range []*castorv1.CastStatus{
		{State: &castorv1.CastStatus_Connecting{Connecting: &emptypb.Empty{}}},
		{State: &castorv1.CastStatus_Connecting{Connecting: &emptypb.Empty{}}},
		{State: &castorv1.CastStatus_Extracting{Extracting: &emptypb.Empty{}}},
	} {
		changed(t.Context(), last, now)
		last = now
	}

	if want := []string{"cast connecting the device", "cast finding streams"}; !slices.Equal(msgs, want) {
		t.Errorf("said %v, want %v", msgs, want)
	}
}

func TestRankingCompletionWithZeroCastableIsAnnounced(t *testing.T) {
	var msgs []string
	prev := slog.Default()
	slog.SetDefault(slog.New(said{msgs: &msgs}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	last := &castorv1.CastStatus{State: &castorv1.CastStatus_Measuring{Measuring: &castorv1.MeasuringStatus{Streams: 2}}}
	now := &castorv1.CastStatus{State: &castorv1.CastStatus_Measuring{Measuring: &castorv1.MeasuringStatus{Streams: 2, Castable: proto.Uint32(0)}}}
	changed(t.Context(), last, now)
	changed(t.Context(), now, proto.CloneOf(now))
	if want := []string{"cast measured"}; !slices.Equal(msgs, want) {
		t.Errorf("said %v, want %v", msgs, want)
	}
}

func TestCastingDoesNotRepeatMeasurementAnnouncements(t *testing.T) {
	var msgs []string
	prev := slog.Default()
	slog.SetDefault(slog.New(said{msgs: &msgs}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	var last *castorv1.CastStatus
	for _, now := range []*castorv1.CastStatus{
		{State: &castorv1.CastStatus_Measuring{Measuring: &castorv1.MeasuringStatus{Streams: 5}}},
		{State: &castorv1.CastStatus_Measuring{Measuring: &castorv1.MeasuringStatus{Streams: 5, Castable: proto.Uint32(3)}}},
		{State: &castorv1.CastStatus_Casting{Casting: &castorv1.CastingStatus{Streams: 5, Castable: 3, Attempt: 1}}},
		{State: &castorv1.CastStatus_Casting{Casting: &castorv1.CastingStatus{Streams: 5, Castable: 3, Attempt: 1, Revision: &castorv1.Revision{Action: castorv1.RecoveryAction_RECOVERY_ACTION_RELAX_READ, Why: "stalled"}}}},
		{State: &castorv1.CastStatus_Casting{Casting: &castorv1.CastingStatus{Streams: 5, Castable: 3, Attempt: 2, Revision: &castorv1.Revision{Action: castorv1.RecoveryAction_RECOVERY_ACTION_RELAX_READ, Why: "stalled"}}}},
	} {
		changed(t.Context(), last, now)
		changed(t.Context(), now, proto.CloneOf(now))
		last = now
	}
	if want := []string{"cast measuring", "cast measured", "cast attempting", "cast revising", "cast attempting"}; !slices.Equal(msgs, want) {
		t.Errorf("said %v, want %v", msgs, want)
	}
}

func TestTypedTerminalResults(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ended *castorv1.Ended
		want  error
	}{
		{"completed", &castorv1.Ended{Result: &castorv1.Ended_Completed{Completed: &emptypb.Empty{}}}, nil},
		{"stopped", &castorv1.Ended{Result: &castorv1.Ended_Stopped{Stopped: &emptypb.Empty{}}}, errStopped},
		{"failed", &castorv1.Ended{Result: &castorv1.Ended_Failed{Failed: &castorv1.Failure{Code: castorv1.FailureCode_FAILURE_CODE_PLAYBACK_FAILED, Message: "stalled"}}}, errors.New("stalled")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := outcome(tc.ended, nil)
			if tc.want == nil {
				if got != nil {
					t.Errorf("outcome = %v, want success", got)
				}
			} else if got == nil || got.Error() != tc.want.Error() {
				t.Errorf("outcome = %v, want %v", got, tc.want)
			}
		})
	}
	transport := errors.New("watch disconnected")
	if got := outcome(nil, transport); !errors.Is(got, transport) {
		t.Errorf("outcome = %v, want watch error", got)
	}
}

func TestDryRunPreservesUnknownBitrate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bitrate *uint64
		resort  bool
		want    string
	}{
		{"unknown", nil, false, "unknown\thttps://cdn.example/video"},
		{"measured", proto.Uint64(8000000), false, "8000000\thttps://cdn.example/video"},
		{"last resort", nil, true, "unknown\thttps://cdn.example/video\tlast resort"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dryRunRow(&castorv1.RankedStream{Url: "https://cdn.example/video", Bitrate: tc.bitrate, LastResort: tc.resort}); got != tc.want {
				t.Errorf("row = %q, want %q", got, tc.want)
			}
		})
	}
}
