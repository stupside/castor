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
	defaultModelName      = "ggml-tiny.en.bin"
	multilingualModelName = "ggml-tiny.bin"
	modelBaseURL          = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main"

	vadModelName    = "ggml-silero-v5.1.2.bin"
	vadModelBaseURL = "https://huggingface.co/ggml-org/whisper-vad/resolve/main"

	downloadTimeout = 10 * time.Minute
)

// cacheDir returns the per-user cache directory for whisper assets, creating it if necessary.
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

func ensureModel(ctx context.Context, configured, language string) (string, error) {
	if configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("whisper.model_path %q: %w", configured, err)
		}
		return configured, nil
	}
	name := defaultModelName
	if language != "en" {
		name = multilingualModelName
	}
	return ensure(ctx, name, modelBaseURL)
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

// downloadFile fetches url into dest atomically; each concurrent download owns its temporary file.
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

	f, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+"-*.part")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return err
	}
	return nil
}
