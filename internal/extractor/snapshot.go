package extractor

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

func snapshot(ctx context.Context, dir, label string) {
	if !slog.Default().Enabled(ctx, slog.LevelDebug) {
		return
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.DebugContext(ctx, "snapshot: mkdir failed", "error", err)
		return
	}

	ts := time.Now().UnixMilli()
	prefix := filepath.Join(dir, fmt.Sprintf("%s_%d", label, ts))

	// Screenshot
	var buf []byte
	if err := chromedp.Run(ctx, chromedp.FullScreenshot(&buf, 90)); err != nil {
		slog.DebugContext(ctx, "snapshot: screenshot failed", "label", label, "error", err)
	} else if err := os.WriteFile(prefix+".png", buf, 0o644); err != nil {
		slog.DebugContext(ctx, "snapshot: write png failed", "error", err)
	}

	// HTML
	var html string
	if err := chromedp.Run(ctx, chromedp.OuterHTML("html", &html)); err != nil {
		slog.DebugContext(ctx, "snapshot: html failed", "label", label, "error", err)
	} else if err := os.WriteFile(prefix+".html", []byte(html), 0o644); err != nil {
		slog.DebugContext(ctx, "snapshot: write html failed", "error", err)
	}

	slog.DebugContext(ctx, "snapshot: saved", "label", label, "path", prefix)
}

// sanitize turns a URL into a safe directory name.
func sanitize(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "unknown"
	}
	s := u.Host + u.Path
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, ":", "_")
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}
