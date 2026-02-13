package scraper

import (
	"context"
	"log/slog"
	"time"

	"github.com/chromedp/chromedp"
)

// turnstileIframePosJS returns the center coordinates of the Turnstile checkbox
// iframe, or null if the Turnstile container or its iframe is not yet visible.
const turnstileIframePosJS = `
(function() {
    const c = document.querySelector('.cf-turnstile');
    if (!c) return null;
    const f = c.querySelector('iframe');
    if (!f) return null;
    const r = f.getBoundingClientRect();
    if (r.width < 10 || r.height < 10) return null;
    return {x: Math.round(r.x + r.width/2), y: Math.round(r.y + r.height/2)};
})()
`

// turnstileGoneJS returns true when the .cf-turnstile element is no longer in
// the DOM (i.e. the page has reloaded after successful verification).
const turnstileGoneJS = `document.querySelector('.cf-turnstile') === null`

// turnstileAutoSolvedJS polls for a non-interactive auto-solve by checking if
// the hidden input cf-turnstile-response has been populated or if
// window.turnstile.getResponse() returns a token.
const turnstileAutoSolvedJS = `
(function() {
    const inp = document.querySelector('input[name="cf-turnstile-response"]');
    if (inp && inp.value && inp.value.length > 0) return true;
    if (window.turnstile && typeof window.turnstile.getResponse === 'function') {
        try { if (window.turnstile.getResponse()) return true; } catch(e) {}
    }
    return false;
})()
`

// waitTurnstile detects a Cloudflare Turnstile challenge on the current page
// and attempts to solve it. It races three paths concurrently:
//  1. Poll for the interactive iframe to appear -> click it
//  2. Poll for auto-solve (non-interactive mode fills the hidden input)
//  3. Wait for the page to navigate (cftCallback reload)
//
// Returns true if a Turnstile was detected and solved.
// The entire flow is bounded by a 20-second timeout.
func waitTurnstile(ctx context.Context) bool {
	var hasTurnstile bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelector('.cf-turnstile') !== null`, &hasTurnstile),
	); err != nil || !hasTurnstile {
		return false
	}

	slog.Debug("playback: turnstile detected, solving")

	tCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	// result channel: first goroutine to send wins.
	type solveResult struct {
		method string
		ok     bool
	}
	ch := make(chan solveResult, 3)

	// Path 1: interactive iframe — poll for it, click, wait for disappearance.
	go func() {
		var pos map[string]any
		if err := chromedp.Run(tCtx,
			chromedp.Poll(turnstileIframePosJS, &pos, chromedp.WithPollingTimeout(0)),
		); err != nil {
			return
		}

		x, _ := pos["x"].(float64)
		y, _ := pos["y"].(float64)

		var solved bool
		if err := chromedp.Run(tCtx,
			logAction("playback: clicking turnstile checkbox", "x", x, "y", y),
			chromedp.MouseClickXY(x, y, chromedp.ButtonLeft),
			snapshot(ctx, "turnstile-click"),
			chromedp.Poll(turnstileGoneJS, &solved, chromedp.WithPollingTimeout(0)),
			chromedp.WaitReady("body"),
			snapshot(ctx, "turnstile-solved"),
		); err != nil {
			return
		}
		ch <- solveResult{"iframe-click", true}
	}()

	// Path 2: auto-solve — poll for the hidden input to be filled.
	go func() {
		var autoSolved bool
		if err := chromedp.Run(tCtx,
			chromedp.Poll(turnstileAutoSolvedJS, &autoSolved, chromedp.WithPollingTimeout(0)),
		); err != nil {
			return
		}
		slog.Debug("playback: turnstile auto-solved, waiting for navigation")

		// After auto-solve the page typically reloads via cftCallback.
		var gone bool
		if err := chromedp.Run(tCtx,
			chromedp.Poll(turnstileGoneJS, &gone, chromedp.WithPollingTimeout(0)),
			chromedp.WaitReady("body"),
			snapshot(ctx, "turnstile-auto-solved"),
		); err != nil {
			return
		}
		ch <- solveResult{"auto-solve", true}
	}()

	// Path 3: page navigation — Turnstile may trigger a full reload.
	go func() {
		// If .cf-turnstile disappears on its own (e.g. JS callback navigates).
		var gone bool
		if err := chromedp.Run(tCtx,
			chromedp.Poll(turnstileGoneJS, &gone, chromedp.WithPollingTimeout(0)),
			chromedp.WaitReady("body"),
		); err != nil {
			return
		}
		ch <- solveResult{"navigation", true}
	}()

	select {
	case res := <-ch:
		cancel() // stop the other goroutines
		slog.Debug("playback: turnstile solved", "method", res.method)
		return res.ok
	case <-tCtx.Done():
		slog.Debug("playback: turnstile solve timed out")
		return false
	}
}
