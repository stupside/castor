package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"time"

	"github.com/stupside/castor/internal/media"
)

const probeTimeout = 30 * time.Second

// ProbeStream runs ffprobe and returns stream info (content type, duration, bit rate).
func ProbeStream(ctx context.Context, ffprobePath string, streamURL *url.URL) (*media.StreamInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_entries", "format=format_name",
		"-show_format",
		streamURL.String(),
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var result struct {
		Format struct {
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
		return nil, err
	}

	return &media.StreamInfo{
		ContentType: contentType,
	}, nil
}
