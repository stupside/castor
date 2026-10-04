package cast

import (
	"context"
	"log/slog"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

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

func TestEachPhaseACastEntersIsAnnouncedOnce(t *testing.T) {
	var msgs []string
	prev := slog.Default()
	slog.SetDefault(slog.New(said{msgs: &msgs}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	var last *castorv1.CastStatus
	for _, p := range []castorv1.Phase{castorv1.Phase_PHASE_CONNECTING, castorv1.Phase_PHASE_CONNECTING, castorv1.Phase_PHASE_EXTRACTING} {
		now := &castorv1.CastStatus{Phase: p}
		changed(t.Context(), last, now)
		last = now
	}

	if want := []string{"cast connecting the device", "cast finding streams"}; !slices.Equal(msgs, want) {
		t.Errorf("said %v, want %v", msgs, want)
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
