package extract

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/chromedp"
)

// snapshot keeps the page at debug level, owner-only: a page can carry session tokens.
func snapshot(ctx context.Context, dir, label string) {
	if dir == "" || ctx.Err() != nil || !slog.Default().Enabled(ctx, slog.LevelDebug) {
		return
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.DebugContext(ctx, "snapshot mkdir failed", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()

	ts := time.Now().UnixMilli()
	prefix := filepath.Join(dir, fmt.Sprintf("%s_%d", label, ts))

	var buf []byte
	if err := chromedp.Run(ctx, chromedp.CaptureScreenshot(&buf)); err != nil {
		slog.DebugContext(ctx, "snapshot screenshot failed", "label", label, "error", err)
	} else if err := os.WriteFile(prefix+".png", buf, 0o600); err != nil {
		slog.DebugContext(ctx, "snapshot png write failed", "error", err)
	}

	var html string
	if err := chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf("document.documentElement.outerHTML.slice(0, %d)", documentSizeLimit), &html)); err != nil {
		slog.DebugContext(ctx, "snapshot html failed", "label", label, "error", err)
	} else if err := os.WriteFile(prefix+".html", []byte(html), 0o600); err != nil {
		slog.DebugContext(ctx, "snapshot html write failed", "error", err)
	}

	slog.DebugContext(ctx, "snapshot saved", "label", label, "path", prefix)
}
