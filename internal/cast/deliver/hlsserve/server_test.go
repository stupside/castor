package hlsserve

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestContentTypeFor(t *testing.T) {
	tests := map[string]string{
		"/stream.m3u8":   "application/vnd.apple.mpegurl",
		"/seg_00001.m4s": "video/iso.segment",
		"/init.mp4":      "video/mp4",
		"/unknown.txt":   "",
	}
	for path, want := range tests {
		if got := contentTypeFor(path); got != want {
			t.Errorf("contentTypeFor(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestServeServesDirWithContentType(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stream.m3u8"), []byte("#EXTM3U\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv, err := New(Config{LocalIP: "127.0.0.1", Dir: dir, Playlist: "stream.m3u8"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer srv.Close()

	resp, err := http.Get(srv.URL().String())
	if err != nil {
		t.Fatalf("GET playlist: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("content-type = %q, want application/vnd.apple.mpegurl", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "#EXTM3U\n" {
		t.Errorf("body = %q, want the playlist contents", string(body))
	}
}

func TestServeMissingFileIs404(t *testing.T) {
	srv, err := New(Config{LocalIP: "127.0.0.1", Dir: t.TempDir(), Playlist: "stream.m3u8"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer srv.Close()

	resp, err := http.Get(srv.URL().String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for missing playlist", resp.StatusCode)
	}
}

func TestWaitReturnsOnContextCancel(t *testing.T) {
	srv, err := New(Config{LocalIP: "127.0.0.1", Dir: t.TempDir(), Playlist: "stream.m3u8"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer srv.Close()

	// Producer never finishes (live), so Wait blocks until ctx ends.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := srv.Wait(ctx); err == nil {
		t.Error("Wait() should return ctx error when the cast is cancelled mid-stream")
	}
}
