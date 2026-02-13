package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os/exec"
	"strconv"
	"time"

	"github.com/stupside/castor/internal/media"
)

// probeStream runs ffprobe and returns stream info (content type, duration, bit rate).
func probeStream(ctx context.Context, ffprobePath string, probeTimeout time.Duration, streamURL *url.URL, headers map[string]string) (*media.StreamInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	args := []string{
		// Suppress non-error output so only JSON is written to stdout.
		// Use "error" (not "quiet") so stderr captures failure details.
		"-v", "error",
		// Output as JSON for structured parsing
		"-print_format", "json",
		// Only request format name and bit rate (minimizes probe work)
		"-show_entries", "format=format_name,bit_rate",
		"-show_format",
	}

	// Forward any HTTP headers (e.g. Referer, User-Agent) to the stream server
	if h := media.FormatHTTPHeaders(headers); h != "" {
		args = append(args, "-headers", h)
	}

	args = append(args, media.HLSInputArgs...)
	args = append(args, streamURL.String())

	slog.DebugContext(ctx, "ffprobe starting", "url", streamURL.String(), "header_count", len(headers))

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
		Format struct {
			BitRate    string `json:"bit_rate"`
			FormatName string `json:"format_name"`
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
			slog.Warn("ffprobe returned non-numeric bit_rate, defaulting to 0", "bit_rate", result.Format.BitRate)
		}
	}

	return &media.StreamInfo{
		BitRate:     bitRate,
		ContentType: contentType,
	}, nil
}
