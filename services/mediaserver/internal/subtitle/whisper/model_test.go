package whisper

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentDownloadsPublishOneCompleteModel(t *testing.T) {
	const readers = 8
	var ready sync.WaitGroup
	ready.Add(readers)
	payload := bytes.Repeat([]byte("model data"), 64<<10)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ready.Done()
		ready.Wait()
		_, _ = w.Write(payload)
	}))
	t.Cleanup(origin.Close)
	dest := filepath.Join(t.TempDir(), "model.bin")
	results := make(chan error, readers)
	for range readers {
		go func() { results <- downloadFile(t.Context(), origin.URL, dest) }()
	}
	for range readers {
		if err := <-results; err != nil {
			t.Errorf("download = %v", err)
		}
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, payload) {
		t.Errorf("published model has %d bytes (%v), want one complete download", len(got), err)
	}
}
