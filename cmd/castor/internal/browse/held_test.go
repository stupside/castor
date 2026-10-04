package browse

import (
	"context"
	"log/slog"
	"slices"
	"testing"
)

type written struct{ lines *[]string }

func (w written) Enabled(context.Context, slog.Level) bool { return true }
func (w written) Handle(_ context.Context, r slog.Record) error {
	*w.lines = append(*w.lines, r.Message)
	return nil
}
func (w written) WithAttrs([]slog.Attr) slog.Handler { return w }
func (w written) WithGroup(string) slog.Handler      { return w }

func TestLinesLoggedUnderAScreenAreWrittenAfterItInOrder(t *testing.T) {
	var out []string
	prev := slog.Default()
	slog.SetDefault(slog.New(written{lines: &out}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	_ = held(func() error {
		slog.Warn("discovery error")
		slog.Info("device found")
		if len(out) != 0 {
			t.Errorf("%v reached the terminal while the screen was up", out)
		}
		return nil
	})

	if want := []string{"discovery error", "device found"}; !slices.Equal(out, want) {
		t.Errorf("wrote %v after the screen, want %v", out, want)
	}
}

func TestALoggerKeptByBackgroundWorkResumesWritingAfterTheScreen(t *testing.T) {
	var out []string
	prev := slog.Default()
	slog.SetDefault(slog.New(written{lines: &out}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	var background *slog.Logger
	_ = held(func() error {
		background = slog.Default().With("work", "discovery")
		background.Warn("screen open")
		return nil
	})
	background.Warn("screen closed")
	if want := []string{"screen open", "screen closed"}; !slices.Equal(out, want) {
		t.Errorf("wrote %v, want the retained logger to resume writing: %v", out, want)
	}
}
