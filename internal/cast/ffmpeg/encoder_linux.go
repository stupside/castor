//go:build linux

package ffmpeg

import (
	"context"
	"os"
)

// hardwareH264 is the Intel VA-API encoder when the render node exists and a
// test encode passes.
func hardwareH264(ctx context.Context, ffmpegPath string) (Encoder, bool) {
	if _, err := os.Stat(vaapiRenderNode); err != nil {
		return Encoder{}, false
	}
	return h264VAAPI, testEncode(ctx, ffmpegPath, h264VAAPI)
}
