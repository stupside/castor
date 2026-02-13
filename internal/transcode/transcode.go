package transcode

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os/exec"
	"strconv"

	"github.com/stupside/castor/internal/app"
	"github.com/stupside/castor/internal/media"
)

// Transcode starts an ffmpeg process that reads from sourceURL and writes
// transcoded output to a pipe. Returns the readable end, a wait function
// for the process, and any startup error.
func Transcode(ctx context.Context, cfg app.TranscodeConfig, sourceURL *url.URL, headers map[string]string) (io.ReadCloser, func() error, error) {
	args := []string{
		// Throttle input reading to roughly real-time playback speed
		"-readrate", strconv.Itoa(cfg.ReadRate),
		// Allow an initial burst of data before rate-limiting kicks in
		"-readrate_initial_burst", strconv.Itoa(cfg.ReadRateBurst),
		// Generate missing PTS and discard corrupt frames
		"-fflags", "+genpts+discardcorrupt",
	}

	// Forward any HTTP headers (e.g. Referer, User-Agent) to the stream server
	if h := media.FormatHTTPHeaders(headers); h != "" {
		args = append(args, "-headers", h)
	}

	args = append(args, media.HLSInputArgs...)
	args = append(args,
		"-i", sourceURL.String(),
		// Video codec (e.g. copy, libx264)
		"-c:v", cfg.VideoCodec,
		// Audio codec (e.g. aac, libmp3lame)
		"-c:a", cfg.AudioCodec,
		// Audio sample rate in Hz
		"-ar", strconv.Itoa(cfg.AudioSampleRate),
		// Audio bitrate (e.g. 128k)
		"-b:a", cfg.AudioBitrate,
		// Container format for the output stream
		"-f", cfg.OutputFormat,
		// Write output to stdout for pipe consumption
		"pipe:1",
	)

	cmd := exec.CommandContext(ctx, cfg.FFmpegPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("creating stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting ffmpeg: %w", err)
	}

	slog.InfoContext(ctx, "ffmpeg process started", "source_url", sourceURL.String(), "video_codec", cfg.VideoCodec, "audio_codec", cfg.AudioCodec, "output_format", cfg.OutputFormat)

	go drainStderr(ctx, stderr)

	return stdout, cmd.Wait, nil
}

// drainStderr reads stderr line-by-line and logs each line at debug level.
func drainStderr(ctx context.Context, r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		slog.DebugContext(ctx, "ffmpeg", "line", scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		slog.WarnContext(ctx, "ffmpeg stderr scanner error", "err", err)
	}
}
