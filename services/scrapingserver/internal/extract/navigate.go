package extract

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// iframeSrcJS finds largest visible iframe (100x100+) and returns src.
//
//go:embed js/iframe_src.js
var iframeSrcJS string

// navigateIframe polls for largest iframe and navigates; repeat until leaf.
func navigateIframe(ctx context.Context) error {
	iframeCtx, cancel := context.WithTimeout(ctx, navigateIframeTimeout)
	defer cancel()

	for depth := range navigateIframeMaxDepth {
		var iframeSrc, parent string

		err := chromedp.Run(iframeCtx,
			chromedp.Poll(iframeSrcJS, &iframeSrc, chromedp.WithPollingTimeout(0)),
			chromedp.Location(&parent),
			chromedp.ActionFunc(func(ctx context.Context) error {
				slog.DebugContext(ctx, "navigating to iframe", "src", iframeSrc, "depth", depth+1)
				return openFrame(ctx, iframeSrc, parent)
			}),
			chromedp.WaitReady("body"),
		)

		if err != nil {
			// No frame at all is a failure; none past the first is the leaf.
			if depth == 0 {
				return err
			}
			return nil
		}
	}

	return nil
}

// openFrame loads a frame's document as its parent would, since embed hosts refuse a request no site framed; only a web page is opened.
func openFrame(ctx context.Context, src, parent string) error {
	if !webPage(src) {
		return fmt.Errorf("frame %q is not a web page", src)
	}
	_, _, errorText, _, err := page.Navigate(src).
		WithReferrer(parent).
		WithReferrerPolicy(page.ReferrerPolicyStrictOriginWhenCrossOrigin).
		Do(ctx)
	if err != nil {
		return err
	}
	if errorText != "" {
		return fmt.Errorf("page load error %s", errorText)
	}
	return nil
}

// webPage is whether src is a page on the web, rather than script, data, a file or the browser's own.
func webPage(src string) bool {
	u, err := url.Parse(src)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https")
}
