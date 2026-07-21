package whisper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	defaultModelName = "ggml-tiny.en.bin"
	modelBaseURL     = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main"

	// vadModelName is the ggml conversion of Silero VAD v5.1.2, the model
	// whisper.cpp's built-in voice activity detection runs.
	vadModelName    = "ggml-silero-v5.1.2.bin"
	vadModelBaseURL = "https://huggingface.co/ggml-org/whisper-vad/resolve/main"

	downloadTimeout = 10 * time.Minute
)

// cacheDir returns the per-user cache directory castor uses for whisper assets,
// creating it if necessary.
func cacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating user cache dir: %w", err)
	}
	dir := filepath.Join(base, "castor", "whisper")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating cache dir %q: %w", dir, err)
	}
	return dir, nil
}

// ensureModel returns a path to a whisper model file. If configured points at
// an existing file it is returned as-is; otherwise the default tiny.en model
// is fetched from Hugging Face into the user's cache directory (a no-op on
// subsequent calls).
func ensureModel(ctx context.Context, configured string) (string, error) {
	if configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("whisper.model_path %q: %w", configured, err)
		}
		return configured, nil
	}
	return ensure(ctx, defaultModelName, modelBaseURL)
}

// ensureVADModel does the same for the Silero VAD model that gates the
// transcriber. It is deliberately not configurable: VAD is an implementation
// detail of the streaming pipeline, not a knob.
func ensureVADModel(ctx context.Context) (string, error) {
	return ensure(ctx, vadModelName, vadModelBaseURL)
}

func ensure(ctx context.Context, name, baseURL string) (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)

	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat model: %w", err)
	}

	url := baseURL + "/" + name
	slog.InfoContext(ctx, "downloading whisper model (one-time)", "name", name, "url", url, "dest", path)
	if err := downloadFile(ctx, url, path); err != nil {
		return "", fmt.Errorf("downloading model: %w", err)
	}
	slog.InfoContext(ctx, "whisper model ready", "path", path)
	return path, nil
}

// downloadFile fetches url into dest atomically: the body is written to a
// sibling .part file and only renamed on success, so interrupted downloads
// don't masquerade as a complete model on the next run.
func downloadFile(ctx context.Context, url, dest string) error {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
