package extractor

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/stupside/castor/internal/action"
	"github.com/stupside/castor/internal/app"
)

// session owns the chromedp lifecycle for a single proxy attempt.
type session struct {
	ctx         context.Context
	cancel      context.CancelFunc
	allocCancel context.CancelFunc
	collector   *collector
	centerX     float64
	centerY     float64
	snapshotDir string
}

// newSession creates a browser session: allocator, stealth injection,
// navigation, and event listeners. It returns a ready-to-use Session.
func newSession(ctx context.Context, e *Extractor, targetURL string) (*session, error) {
	profile := NewProfile()

	opts := allocatorOpts(e.browser, profile)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)

	taskCtx, taskCancel := chromedp.NewContext(allocCtx)

	collector := newCollector(e.patterns, e.capture.MaxCandidates)

	chromedp.ListenTarget(taskCtx, collector.Listen)

	// Navigate with a timeout, but don't use a child context — canceling a
	// child of the chromedp task context breaks the target in chromedp v0.14.
	navDone := make(chan error, 1)
	go func() {
		navDone <- chromedp.Run(taskCtx,
			runtime.Enable(),
			network.Enable(),
			browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorDeny),
			injectStealth(profile),
			injectCDPStealth(profile),
			chromedp.Navigate(targetURL),
		)
	}()

	var err error
	select {
	case err = <-navDone:
		// Navigation completed (success or error).
	case <-time.After(e.browser.Timeout):
		err = fmt.Errorf("navigation timed out after %s", e.browser.Timeout)
	}

	if err != nil {
		// If navigation failed but we already captured URLs, keep going.
		if !collector.HasHits() {
			taskCancel()
			allocCancel()
			return nil, err
		}
	}

	snapDir := filepath.Join(".debug", sanitize(targetURL))
	snapshot(taskCtx, snapDir, "after_nav")

	return &session{
		ctx:         taskCtx,
		cancel:      taskCancel,
		allocCancel: allocCancel,
		collector:   collector,
		centerX:     profile.CenterX,
		centerY:     profile.CenterY,
		snapshotDir: snapDir,
	}, nil
}

// RunActions executes the action pipeline, skipping remaining steps once URLs are captured.
func (s *session) RunActions(actionCfg app.ActionConfig) {
	snapshot(s.ctx, s.snapshotDir, "pipeline_start")

	if !s.collector.HasHits() {
		if err := action.Click(s.ctx, s.centerX, s.centerY); err != nil {
			slog.DebugContext(s.ctx, "pipeline: click center failed", "error", err)
		}
		snapshot(s.ctx, s.snapshotDir, "step_0")
	}

	if !s.collector.HasHits() {
		if err := action.NavigateIframe(s.ctx, actionCfg.NavigateIframeTimeout, actionCfg.NavigateIframeMaxDepth); err != nil {
			slog.DebugContext(s.ctx, "pipeline: navigate iframe failed", "error", err)
		}
		snapshot(s.ctx, s.snapshotDir, "step_1")
	}

	if !s.collector.HasHits() {
		if err := action.BypassTurnstile(s.ctx, actionCfg.BypassTurnstileTimeout, actionCfg.TurnstileRetryTimeout); err != nil {
			slog.DebugContext(s.ctx, "pipeline: bypass turnstile failed", "error", err)
		}
		snapshot(s.ctx, s.snapshotDir, "step_2")
	}

	if !s.collector.HasHits() {
		if err := action.Click(s.ctx, s.centerX, s.centerY); err != nil {
			slog.DebugContext(s.ctx, "pipeline: click center failed", "error", err)
		}
		snapshot(s.ctx, s.snapshotDir, "step_3")
	}
}

// Close tears down the browser and allocator.
func (s *session) Close() {
	s.cancel()
	s.allocCancel()
}
