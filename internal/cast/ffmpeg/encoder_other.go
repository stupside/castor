//go:build !linux && !darwin

package ffmpeg

import "context"

// hardwareH264 has no backend on other platforms: selection falls back to
// libx264.
func hardwareH264(context.Context, string) (Encoder, bool) { return Encoder{}, false }
