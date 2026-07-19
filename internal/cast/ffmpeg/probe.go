package ffmpeg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/stupside/castor/internal/media"
)

// Probe runs ffprobe against input (a URL or a local file path) and returns the
// per-track codec details as a media.ProbeInfo (the domain type). It is safe to
// point at a still-growing local spool: ffprobe reads from the start, analyses
// the leading packets, and returns.
func Probe(ctx context.Context, ffprobePath, input string) (media.ProbeInfo, error) {
	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_entries",
		"stream=codec_type,codec_name,profile,level,width,height,pix_fmt,color_transfer,channels:format=bit_rate",
		input,
	}

	out, err := exec.CommandContext(ctx, ffprobePath, args...).Output()
	if err != nil {
		var e *exec.ExitError
		if errors.As(err, &e) && len(e.Stderr) > 0 {
			return media.ProbeInfo{}, fmt.Errorf("ffprobe: %w\n%s", err, e.Stderr)
		}
		return media.ProbeInfo{}, fmt.Errorf("ffprobe: %w", err)
	}

	var result struct {
		Streams []struct {
			CodecType     string `json:"codec_type"`
			CodecName     string `json:"codec_name"`
			Profile       string `json:"profile"`
			Level         int    `json:"level"`
			Width         int    `json:"width"`
			Height        int    `json:"height"`
			PixFmt        string `json:"pix_fmt"`
			ColorTransfer string `json:"color_transfer"`
			Channels      int    `json:"channels"`
		} `json:"streams"`
		Format struct {
			BitRate string `json:"bit_rate"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return media.ProbeInfo{}, fmt.Errorf("parsing ffprobe output: %w", err)
	}

	var info media.ProbeInfo
	for _, s := range result.Streams {
		switch s.CodecType {
		case "video":
			// Guard against decoy streams and multiple video tracks: keep the
			// first real one and ignore later thumbnails.
			if info.VideoCodec != "" {
				continue
			}
			info.VideoCodec = media.Codec(s.CodecName)
			info.VideoProfile = s.Profile
			info.VideoLevel = s.Level
			info.VideoWidth = s.Width
			info.VideoHeight = s.Height
			info.VideoBitDepth = pixFmtBitDepth(s.PixFmt)
			info.VideoHDR = isHDRTransfer(s.ColorTransfer)
		case "audio":
			if info.AudioCodec != "" {
				continue
			}
			info.AudioCodec = s.CodecName
			info.AudioChannels = s.Channels
		}
	}
	if result.Format.BitRate != "" {
		info.BitRate, _ = strconv.ParseInt(result.Format.BitRate, 10, 64)
	}
	return info, nil
}

// pixFmtBitDepth derives the luma bit depth from an ffprobe pix_fmt name.
// 8-bit formats (yuv420p, nv12, yuvj420p) carry no depth marker; 10/12-bit
// ones do (yuv420p10le, p010le, yuv422p12le).
func pixFmtBitDepth(pixFmt string) int {
	switch {
	case pixFmt == "":
		return 0
	case strings.Contains(pixFmt, "12"):
		return 12
	case strings.Contains(pixFmt, "10"):
		return 10
	default:
		return 8
	}
}

// isHDRTransfer reports whether an ffprobe color_transfer names an HDR curve.
func isHDRTransfer(transfer string) bool {
	switch transfer {
	case "smpte2084", "arib-std-b67":
		return true
	default:
		return false
	}
}
