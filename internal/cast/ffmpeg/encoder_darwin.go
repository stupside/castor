//go:build darwin

package ffmpeg

import "context"

// hardwareH264 is Apple VideoToolbox when a test encode passes.
func hardwareH264(ctx context.Context, ffmpegPath string) (Encoder, bool) {
	return h264VideoToolbox, testEncode(ctx, ffmpegPath, h264VideoToolbox)
}
