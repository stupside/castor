package action

import (
	"context"

	"github.com/chromedp/chromedp"
)

// Click clicks at the given viewport coordinates.
func Click(ctx context.Context, x, y float64) error {
	return chromedp.Run(ctx, chromedp.MouseClickXY(x, y, chromedp.ButtonLeft))
}
