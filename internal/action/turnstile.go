package action

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"time"

	"github.com/chromedp/chromedp"
)

// turnstileIframePosJS returns the center coordinates of the Turnstile checkbox
// iframe, or null if the Turnstile container or its iframe is not yet visible.
//
//go:embed js/turnstile_iframe_pos.js
var turnstileIframePosJS string

// turnstileGoneJS returns true when the .cf-turnstile element is no longer in
// the DOM (i.e. the page has reloaded after successful verification).
//
//go:embed js/turnstile_gone.js
var turnstileGoneJS string

// detectTurnstile returns true if a .cf-turnstile element is present in the DOM.
func detectTurnstile(ctx context.Context) bool {
	var present bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelector('.cf-turnstile') !== null`, &present),
	); err != nil {
		return false
	}
	return present
}

// solveTurnstile attempts to solve a Cloudflare Turnstile challenge on the
// current page. It races two paths concurrently:
//  1. Poll for the interactive iframe to appear -> click it -> wait for gone
//  2. Wait for turnstile to disappear (covers auto-solve and cftCallback reload)
//
// The caller should check detectTurnstile before calling this function.
// The entire flow is bounded by solveTimeout.
func solveTurnstile(ctx context.Context, solveTimeout time.Duration) bool {
	tCtx, cancel := context.WithTimeout(ctx, solveTimeout)
	defer cancel()

	ch := make(chan struct{}, 2)

	// Path 1: interactive iframe click.
	go func() {
		var pos map[string]any
		if err := chromedp.Run(tCtx,
			chromedp.Poll(turnstileIframePosJS, &pos, chromedp.WithPollingTimeout(0)),
		); err != nil {
			slog.DebugContext(ctx, "error polling for turnstile iframe", "error", err)
			return
		}

		x, _ := pos["x"].(float64)
		y, _ := pos["y"].(float64)

		var gone bool
		if err := chromedp.Run(tCtx,
			chromedp.MouseClickXY(x, y, chromedp.ButtonLeft),
			chromedp.Poll(turnstileGoneJS, &gone, chromedp.WithPollingTimeout(0)),
			chromedp.WaitReady("body"),
		); err != nil {
			slog.DebugContext(ctx, "error solving turnstile", "error", err)
			return
		}
		ch <- struct{}{}
	}()

	// Path 2: passive — handles auto-solve token fill and cftCallback page reload.
	go func() {
		var gone bool
		if err := chromedp.Run(tCtx,
			chromedp.Poll(turnstileGoneJS, &gone, chromedp.WithPollingTimeout(0)),
			chromedp.WaitReady("body"),
		); err != nil {
			slog.DebugContext(ctx, "error polling for turnstile gone", "error", err)
			return
		}
		ch <- struct{}{}
	}()

	select {
	case <-ch:
		cancel()
		return true
	case <-tCtx.Done():
		return false
	}
}

// BypassTurnstile attempts to bypass a Cloudflare Turnstile challenge.
func BypassTurnstile(ctx context.Context, solveTimeout, retryTimeout time.Duration) error {
	if !detectTurnstile(ctx) {
		return nil
	}

	if solveTurnstile(ctx, solveTimeout) {
		return nil
	} else {
		slog.DebugContext(ctx, "initial turnstile solve attempt failed, retrying after reload")
	}

	retryCtx, retryCancel := context.WithTimeout(ctx, retryTimeout)
	defer retryCancel()
	if err := chromedp.Run(retryCtx, chromedp.Reload(), chromedp.WaitReady("body")); err != nil {
		return fmt.Errorf("turnstile reload failed: %w", err)
	}

	if detectTurnstile(ctx) {
		if !solveTurnstile(ctx, solveTimeout) {
			return fmt.Errorf("turnstile solve failed after retry")
		}
		slog.DebugContext(ctx, "turnstile solved after retry")
	}

	return nil
}
