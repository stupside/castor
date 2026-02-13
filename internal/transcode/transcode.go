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
)

// TranscodeConfig holds ffmpeg transcode settings.
type TranscodeConfig struct {
	ReadRate        int    `koanf:"read_rate" validate:"required"`
	ReadRateBurst   int    `koanf:"read_rate_burst" validate:"required"`
	VideoCodec      string `koanf:"video_codec" validate:"required"`
	AudioCodec      string `koanf:"audio_codec" validate:"required"`
	AudioSampleRate int    `koanf:"audio_sample_rate" validate:"required"`
	AudioBitrate    string `koanf:"audio_bitrate" validate:"required"`
	OutputFormat    string `koanf:"output_format" validate:"required"`
}

// Config combines the ffmpeg binary path with transcode settings.
type Config struct {
	FFmpegPath string
	Transcode  TranscodeConfig
}

// Transcode starts an ffmpeg process that reads from sourceURL and writes
// transcoded output to a pipe. Returns the readable end, a wait function
// for the process, and any startup error.
func Transcode(ctx context.Context, cfg Config, sourceURL *url.URL) (io.ReadCloser, func() error, error) {
	tc := cfg.Transcode

	args := []string{
		"-readrate", strconv.Itoa(tc.ReadRate),
		"-readrate_initial_burst", strconv.Itoa(tc.ReadRateBurst),
		"-fflags", "+genpts+discardcorrupt",
		"-i", sourceURL.String(),
	}

	args = append(args,
		"-c:v", tc.VideoCodec,
		"-c:a", tc.AudioCodec,
	)

	args = append(args,
		"-ar", strconv.Itoa(tc.AudioSampleRate),
		"-b:a", tc.AudioBitrate,
		"-f", tc.OutputFormat,
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

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			slog.Debug("ffmpeg", "line", scanner.Text())
		}
	}()

	return stdout, cmd.Wait, nil
}
