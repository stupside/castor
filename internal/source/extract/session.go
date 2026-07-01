package extract

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

	collector := newCollector(taskCtx, e.patterns, e.capture.MaxCandidates)

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

// RunActions executes the action pipeline, skipping remaining steps once URLs
// are captured. Each step is best-effort: failures are logged at DEBUG and
// the next step still runs.
func (s *session) RunActions(actionCfg ActionConfig) {
	snapshot(s.ctx, s.snapshotDir, "pipeline_start")

	steps := []struct {
		name string
		do   func() error
	}{
		{"click", func() error { return click(s.ctx, s.centerX, s.centerY) }},
		{"navigate iframe", func() error {
			return navigateIframe(s.ctx, actionCfg.NavigateIframeTimeout, actionCfg.NavigateIframeMaxDepth)
		}},
		{"bypass turnstile", func() error {
			return bypassTurnstile(s.ctx, actionCfg.BypassTurnstileTimeout, actionCfg.TurnstileRetryTimeout)
		}},
		{"click", func() error { return click(s.ctx, s.centerX, s.centerY) }},
	}

	for i, step := range steps {
		if s.collector.HasHits() {
			return
		}
		if err := step.do(); err != nil {
			slog.DebugContext(s.ctx, step.name+" failed", "error", err)
		}
		snapshot(s.ctx, s.snapshotDir, fmt.Sprintf("step_%d", i))
	}
}

// Close tears down the browser and allocator.
func (s *session) Close() {
	s.cancel()
	s.allocCancel()
}
