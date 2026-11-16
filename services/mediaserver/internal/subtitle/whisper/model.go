package whisper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	defaultModelName      = "ggml-base.en.bin"
	multilingualModelName = "ggml-base.bin"
	modelBaseURL          = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main"

	vadModelName    = "ggml-silero-v5.1.2.bin"
	vadModelBaseURL = "https://huggingface.co/ggml-org/whisper-vad/resolve/main"

	downloadTimeout = 10 * time.Minute
	// maxModelBytes bounds a download before its checksum is known; the largest pinned model is about 148 MB.
	maxModelBytes = 256 << 20
)

// modelSHA256 pins each model to the LFS digest Hugging Face publishes for it.
var modelSHA256 = map[string]string{
	defaultModelName:      "a03779c86df3323075f5e796cb2ce5029f00ec8869eee3fdfb897afe36c6d002",
	multilingualModelName: "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe",
	vadModelName:          "29940d98d42b91fbd05ce489f3ecf7c72f0a42f027e4875919a28fb4c04ea2cf",
}

// cacheDir returns the per-user cache directory for whisper assets, creating it if necessary.
func cacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating user cache dir: %w", err)
	}
	dir := filepath.Join(base, "castor", "whisper")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating cache dir %q: %w", dir, err)
	}
	return dir, nil
}

func ensureModel(ctx context.Context, configured, language string) (string, error) {
	if configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("servers.media.whisper.model_path %q: %w", configured, err)
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

	sum, ok := modelSHA256[name]
	if !ok {
		return "", fmt.Errorf("no pinned checksum for model %q", name)
	}
	url := baseURL + "/" + name
	slog.InfoContext(ctx, "downloading whisper model (one-time)", "name", name, "url", url, "dest", path)
	if err := downloadFile(ctx, url, path, sum); err != nil {
		return "", fmt.Errorf("downloading model: %w", err)
	}
	slog.InfoContext(ctx, "whisper model ready", "path", path)
	return path, nil
}

// downloadFile fetches url into dest atomically once its body has the hex SHA-256 sum; each concurrent download owns its temporary file.
func downloadFile(ctx context.Context, url, dest, sum string) error {
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
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxModelBytes+1))
	if err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if n > maxModelBytes {
		return fmt.Errorf("larger than %d bytes", maxModelBytes)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, sum)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return err
	}
	return nil
}
