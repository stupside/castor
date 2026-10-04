package browse

import (
	"context"
	"log/slog"
	"slices"
	"sync"
)

// held runs a full-screen program with this process's log lines held back, then writes them below it.
func held(run func() error) error {
	prev := slog.Default()
	h := &holding{next: prev.Handler(), lines: &lines{}}
	slog.SetDefault(slog.New(h))
	defer func() {
		slog.SetDefault(prev)
		h.lines.mu.Lock()
		defer h.lines.mu.Unlock()
		for _, r := range h.lines.kept {
			_ = prev.Handler().Handle(context.Background(), r)
		}
		h.lines.kept = nil
		h.lines.released = true
	}()
	return run()
}

// holding keeps what next would have written, in order, instead of writing it.
type holding struct {
	next  slog.Handler
	attrs []slog.Attr
	lines *lines
}

type lines struct {
	mu       sync.Mutex
	kept     []slog.Record
	released bool
}

func (h *holding) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *holding) Handle(ctx context.Context, r slog.Record) error {
	r = r.Clone()
	r.AddAttrs(h.attrs...)
	h.lines.mu.Lock()
	if h.lines.released {
		h.lines.mu.Unlock()
		return h.next.Handle(ctx, r)
	}
	h.lines.kept = append(h.lines.kept, r)
	h.lines.mu.Unlock()
	return nil
}

func (h *holding) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &holding{next: h.next, attrs: slices.Concat(h.attrs, attrs), lines: h.lines}
}

// WithGroup is unused by castor's screens; lines keep their keys flat.
func (h *holding) WithGroup(string) slog.Handler { return h }
