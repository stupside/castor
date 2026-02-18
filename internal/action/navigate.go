package action

import (
	"context"
	_ "embed"
	"log/slog"
	"time"

	"github.com/chromedp/chromedp"
)

// iframeSrcJS finds the largest visible iframe (min 100x100) and returns its
// src URL. Returns null if no suitable iframe is found or src is empty/about:.
//
//go:embed js/iframe_src.js
var iframeSrcJS string

// NavigateIframe polls for the largest iframe and navigates into it,
// repeating through nested iframes until no more are found.
func NavigateIframe(ctx context.Context, timeout time.Duration, maxDepth int) error {
	iframeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for depth := range maxDepth {
		var iframeSrc string

		err := chromedp.Run(iframeCtx,
			chromedp.Poll(iframeSrcJS, &iframeSrc, chromedp.WithPollingTimeout(0)),
			chromedp.ActionFunc(func(ctx context.Context) error {
				slog.DebugContext(ctx, "navigating to iframe", "src", iframeSrc, "depth", depth+1)
				return chromedp.Navigate(iframeSrc).Do(ctx)
			}),
			chromedp.WaitReady("body"),
		)

		if err != nil {
			if depth == 0 {
				return err // No iframe found at all — real error
			}
			return nil // Reached leaf — no more iframes, that's fine
		}
	}

	return nil
}
