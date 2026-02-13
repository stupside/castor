package action

import (
	"context"

	"github.com/chromedp/chromedp"
)

// ClickCenter clicks at the given viewport coordinates.
func ClickCenter(ctx context.Context, x, y float64) error {
	return chromedp.Run(ctx, chromedp.MouseClickXY(x, y, chromedp.ButtonLeft))
}
