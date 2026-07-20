package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/stupside/castor/internal/media"
)

// probeStream runs ffprobe and returns stream info (content type, duration, bit rate).
func probeStream(ctx context.Context, ffprobePath string, probeTimeout time.Duration, streamURL *url.URL, headers http.Header) (*media.StreamInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	args := []string{
		// Suppress non-error output so only JSON is written to stdout.
		// Use "error" (not "quiet") so stderr captures failure details.
		"-v", "error",
		// Output as JSON for structured parsing
		"-print_format", "json",
		// Format name + bit rate identify the container and rank quality;
		// duration separates a feature title from a spliced-in pre-roll ad;
		// the per-stream codec/type/dimensions let us reject decoy playlists
		// (image-only "video", no audio) that would crash the puller's
		// stream mapping.
		"-show_entries", "format=format_name,bit_rate,duration:stream=codec_type,codec_name,width,height",
	}

	// Forward any HTTP headers (e.g. Referer, User-Agent) to the stream server
	args = append(args, headerArgs(headers)...)
	args = append(args, media.HLSInputArgs...)
	args = append(args, streamURL.String())

	slog.DebugContext(ctx, "running ffprobe", "url", streamURL.String(), "header_count", len(headers))

	cmd := exec.CommandContext(ctx, ffprobePath, args...)

	out, err := cmd.Output()
	if err != nil {
		var e *exec.ExitError
		if errors.As(err, &e) && len(e.Stderr) > 0 {
			return nil, fmt.Errorf("ffprobe: %w\n%s", err, e.Stderr)
		}
		return nil, fmt.Errorf("ffprobe: %w", err)
	}

	var result struct {
		Streams []struct {
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			CodecName string `json:"codec_name"`
			CodecType string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			BitRate    string `json:"bit_rate"`
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing ffprobe output: %w", err)
	}

	if result.Format.FormatName == "" {
		return nil, fmt.Errorf("ffprobe returned no format name")
	}

	contentType, err := media.FormatToContentType(result.Format.FormatName)
	if err != nil {
		return nil, fmt.Errorf("mapping format %q: %w", result.Format.FormatName, err)
	}

	var bitRate int64
	if result.Format.BitRate != "" {
		bitRate, err = strconv.ParseInt(result.Format.BitRate, 10, 64)
		if err != nil {
			slog.WarnContext(ctx, "ffprobe returned non-numeric bit_rate, defaulting to 0", "bit_rate", result.Format.BitRate)
		}
	}

	info := &media.StreamInfo{BitRate: bitRate, ContentType: contentType}

	// Fractional seconds ("5405.400000"), or absent/"N/A" for live streams.
	// Unparseable stays zero, which callers read as "unknown", not "short".
	if secs, err := strconv.ParseFloat(result.Format.Duration, 64); err == nil && secs > 0 {
		info.Duration = time.Duration(secs * float64(time.Second))
	}

	for _, s := range result.Streams {
		switch s.CodecType {
		case "video":
			// A real video track has dimensions and a non-image codec. Decoy
			// playlists carry a single png/mjpeg "video" with no size.
			if s.Width > 0 && s.Height > 0 && !isImageCodec(s.CodecName) {
				info.HasVideo = true
				if info.VideoHeight == 0 {
					info.VideoHeight = s.Height
				}
			}
		case "audio":
			info.HasAudio = true
		}
	}
	return info, nil
}

// headerArgs renders h as the ffprobe -headers flag pair, or nil when h is
// empty. ffprobe wants every request header in one CRLF-joined blob, the same
// shape ffmpeg's -headers takes.
func headerArgs(h http.Header) []string {
	if len(h) == 0 {
		return nil
	}
	var b strings.Builder
	for key, values := range h {
		for _, v := range values {
			b.WriteString(key)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\r\n")
		}
	}
	return []string{"-headers", b.String()}
}

// imageCodecs are ffmpeg codec names that decode to a still image rather than
// motion video. A playlist whose only "video" track is one of these is a decoy.
var imageCodecs = map[string]bool{
	"png": true, "apng": true, "mjpeg": true, "jpeg": true, "jpegls": true,
	"bmp": true, "gif": true, "tiff": true, "webp": true, "ppm": true,
}

func isImageCodec(name string) bool { return imageCodecs[name] }
