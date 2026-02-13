package scraper

import (
	"context"
	"log/slog"
	"time"

	"github.com/chromedp/chromedp"
)

// iframeSrcJS finds the largest visible iframe (min 100×100) and returns its
// src URL. Returns null if no suitable iframe is found or src is empty/about:.
const iframeSrcJS = `
(function() {
  const iframes = document.querySelectorAll('iframe');
  let best = null, maxArea = 0;
  for (const f of iframes) {
    const r = f.getBoundingClientRect();
    const a = r.width * r.height;
    if (a > maxArea && r.width > 100 && r.height > 100) { maxArea = a; best = f; }
  }
  if (best && best.src && !best.src.startsWith('about:')) return best.src;
  return null;
})()
`

// logAction returns an Action that emits a debug log line.
func logAction(msg string, args ...any) chromedp.ActionFunc {
	return func(_ context.Context) error {
		slog.Debug(msg, args...)
		return nil
	}
}

// navigateTo returns an Action that navigates to the URL stored in *src.
// Pointer indirection is required: the action chain is constructed before
// execution, so *src is empty at build time. Poll populates it before
// this action runs.
func navigateTo(src *string) chromedp.ActionFunc {
	return func(ctx context.Context) error {
		slog.Debug("playback: navigating to player iframe", "url", *src)
		return chromedp.Navigate(*src).Do(ctx)
	}
}

// snapshot returns an Action that captures a debug snapshot. Uses the
// parent context (not the executor context) so snapshots work even after
// timeout cancellation.
func snapshot(ctx context.Context, label string) chromedp.ActionFunc {
	return func(_ context.Context) error {
		debugSnapshot(ctx, label)
		return nil
	}
}

// triggerPlayback dismisses any overlay with a viewport center-click, extracts
// the player iframe's src URL, navigates directly to it, handles a potential
// Cloudflare Turnstile challenge, then clicks to start playback. Navigating to
// the iframe URL makes the player the main page, eliminating cross-origin
// barriers.
func triggerPlayback(ctx context.Context, profile *Profile) {
	// Phase 1 — Find & navigate to iframe.
	phase1Ctx, phase1Cancel := context.WithTimeout(ctx, 5*time.Second)
	defer phase1Cancel()

	var iframeSrc string

	err := chromedp.Run(phase1Ctx,
		// Dismiss overlay / trigger player load.
		logAction("playback: clicking viewport center"),
		chromedp.MouseClickXY(profile.CenterX, profile.CenterY, chromedp.ButtonLeft),
		snapshot(ctx, "center-click"),

		// Wait for the largest iframe to acquire a src URL.
		// Polling runs in-browser via requestAnimationFrame;
		// WithPollingTimeout(0) disables Poll's internal 30s timeout —
		// the 5s context deadline is the effective limit.
		logAction("playback: polling for player iframe src"),
		chromedp.Poll(iframeSrcJS, &iframeSrc, chromedp.WithPollingTimeout(0)),

		// Navigate to the iframe URL (pointer is now populated by Poll).
		navigateTo(&iframeSrc),
		chromedp.WaitReady("body"),
		snapshot(ctx, "iframe-direct"),
	)
	if err != nil {
		slog.Debug("playback: phase 1 (iframe navigation) failed", "error", err)
		return
	}

	// Phase 2 — Handle Turnstile challenge if present.
	// If the first attempt fails (Turnstile didn't render), reload once and retry.
	if !waitTurnstile(ctx) {
		// Check if there actually was a Turnstile before retrying.
		var hasTurnstile bool
		_ = chromedp.Run(ctx,
			chromedp.Evaluate(`document.querySelector('.cf-turnstile') !== null`, &hasTurnstile),
		)
		if hasTurnstile {
			slog.Debug("playback: turnstile retry — reloading page")
			retryCtx, retryCancel := context.WithTimeout(ctx, 5*time.Second)
			defer retryCancel()
			if err := chromedp.Run(retryCtx, chromedp.Reload(), chromedp.WaitReady("body")); err == nil {
				waitTurnstile(ctx)
			}
		}
	}

	// Phase 3 — Start playback.
	phase3Ctx, phase3Cancel := context.WithTimeout(ctx, 5*time.Second)
	defer phase3Cancel()

	err = chromedp.Run(phase3Ctx,
		logAction("playback: clicking center to start playback"),
		chromedp.MouseClickXY(profile.CenterX, profile.CenterY, chromedp.ButtonLeft),
		snapshot(ctx, "playback-click"),
	)
	if err != nil {
		slog.Debug("playback: phase 3 (start playback) failed", "error", err)
	}
}
