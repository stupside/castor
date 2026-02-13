package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/chromedp"
)

var (
	snapshotDir  string
	snapshotOnce sync.Once
	snapshotSeq  atomic.Int64
)

// debugSnapshot captures a screenshot and outer HTML of the current page and
// writes them to a temporary directory. It is a no-op when the default logger
// does not have debug level enabled. Errors are logged and swallowed so
// snapshots never break the main flow.
func debugSnapshot(ctx context.Context, label string) {
	if !slog.Default().Enabled(ctx, slog.LevelDebug) {
		return
	}

	snapshotOnce.Do(func() {
		snapshotDir = filepath.Join(".debug", fmt.Sprintf("castor-debug-%d", time.Now().UnixMilli()))
		if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
			slog.Debug("snapshot: failed to create debug directory", "error", err)
			return
		}
		slog.Debug("snapshot: debug directory created", "path", snapshotDir)
	})

	if snapshotDir == "" {
		return
	}

	seq := snapshotSeq.Add(1)
	prefix := filepath.Join(snapshotDir, fmt.Sprintf("%02d-%s", seq, label))

	// Capture screenshot.
	var buf []byte
	if err := chromedp.Run(ctx, chromedp.CaptureScreenshot(&buf)); err != nil {
		slog.Debug("snapshot: screenshot failed", "label", label, "error", err)
	} else {
		pngPath := prefix + ".png"
		if err := os.WriteFile(pngPath, buf, 0o644); err != nil {
			slog.Debug("snapshot: failed to write screenshot", "path", pngPath, "error", err)
		} else {
			slog.Debug("snapshot: saved", "path", pngPath)
		}
	}

	// Capture outer HTML.
	var html string
	if err := chromedp.Run(ctx, chromedp.OuterHTML("html", &html)); err != nil {
		slog.Debug("snapshot: outer HTML failed", "label", label, "error", err)
	} else {
		htmlPath := prefix + ".html"
		if err := os.WriteFile(htmlPath, []byte(html), 0o644); err != nil {
			slog.Debug("snapshot: failed to write HTML", "path", htmlPath, "error", err)
		} else {
			slog.Debug("snapshot: saved", "path", htmlPath)
		}
	}
}
