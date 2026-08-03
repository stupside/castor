package ffmpeg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/stupside/castor/internal/media"
)

// ProbeFile probes a local file and returns its per-track codec details. It is
// safe to point at a still-growing spool: ffprobe reads from the start, analyses
// the leading packets, and returns.
func ProbeFile(ctx context.Context, ffprobePath, path string) (media.ProbeInfo, error) {
	return probe(ctx, ffprobePath, path, nil)
}

// ProbeSource probes an upstream over the network. It opens the source exactly
// as the reader that follows it will (same request headers, same
// container-specific input flags), so it cannot fail where the read would
// succeed. That matters for a playlist whose segments are served under a
// disguised extension: without the relaxed extension checks the probe reports an
// unreadable stream while the remux plays it fine.
func ProbeSource(ctx context.Context, ffprobePath string, src NetworkSource) (media.ProbeInfo, error) {
	args := media.HeaderArgs(src.Headers)
	args = append(args, containerInputArgs(src.ContentType)...)
	return probe(ctx, ffprobePath, src.URL.String(), args)
}

// probe runs ffprobe against input and maps its JSON to the domain ProbeInfo.
// inputArgs are the flags an input needs to be opened at all (headers, container
// leniency); everything the decision layer reads is in the -show_entries list.
func probe(ctx context.Context, ffprobePath, input string, inputArgs []string) (media.ProbeInfo, error) {
	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_entries",
		"stream=codec_type,codec_name,profile,height,pix_fmt,color_transfer,channels",
	}
	args = append(args, inputArgs...)
	args = append(args, input)

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
			Height        int    `json:"height"`
			PixFmt        string `json:"pix_fmt"`
			ColorTransfer string `json:"color_transfer"`
			Channels      int    `json:"channels"`
		} `json:"streams"`
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
			info.VideoHeight = s.Height
			info.VideoBitDepth = pixFmtBitDepth(s.PixFmt)
			info.VideoHDR = isHDRTransfer(s.ColorTransfer)
		case "audio":
			// Keep the first audio track (the default the pull maps as 0:a:0),
			// ignoring later alternates/commentary.
			if info.AudioCodec != "" {
				continue
			}
			info.AudioCodec = media.Codec(s.CodecName)
			info.AudioChannels = s.Channels
		}
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
