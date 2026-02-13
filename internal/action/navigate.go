package action

import (
	"context"
	_ "embed"
	"time"

	"github.com/chromedp/chromedp"
)

// iframeSrcJS finds the largest visible iframe (min 100x100) and returns its
// src URL. Returns null if no suitable iframe is found or src is empty/about:.
//
//go:embed js/iframe_src.js
var iframeSrcJS string

// NavigateIframe polls for the largest iframe and navigates to it.
func NavigateIframe(ctx context.Context, timeout time.Duration) error {
	iframeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var iframeSrc string
	return chromedp.Run(iframeCtx,
		chromedp.Poll(iframeSrcJS, &iframeSrc, chromedp.WithPollingTimeout(0)),
		// Pointer indirection is required: the action chain is constructed before
		// execution, so iframeSrc is empty at build time.
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.Navigate(iframeSrc).Do(ctx)
		}),
		chromedp.WaitReady("body"),
	)
}
