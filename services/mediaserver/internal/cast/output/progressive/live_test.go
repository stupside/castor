package progressive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"

	"golang.org/x/sync/semaphore"
)

func liveSpool(t *testing.T, window int64) (*storage.Spool, *semaphore.Weighted) {
	t.Helper()
	budget := semaphore.NewWeighted(window * 2)
	s, err := storage.NewLiveSpoolWithBudget(filepath.Join(t.TempDir(), "live.ts"), window, budget)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, budget
}

func TestLiveHTTPRetainedRangesAndExpiredReconnect(t *testing.T) {
	s, _ := liveSpool(t, 8)
	if _, err := s.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	stream := ServeSpool(output.Opening{}, s)
	for _, tt := range []struct {
		rangeHeader  string
		status       int
		body         string
		contentRange string
	}{
		{"", http.StatusGone, "", ""},
		{"bytes=0-", http.StatusGone, "", ""},
		{"bytes=9-12", http.StatusPartialContent, "9abc", "bytes 9-12/*"},
		{"bytes=13-", http.StatusPartialContent, "def", "bytes 13-15/*"},
		{"bytes=99-", http.StatusRequestedRangeNotSatisfiable, "", ""},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://example/stream", nil)
		req.Header.Set("Range", tt.rangeHeader)
		recorder := httptest.NewRecorder()
		stream.handleStream(recorder, req.WithContext(t.Context()))
		if recorder.Code != tt.status {
			t.Fatalf("%s status=%d", tt.rangeHeader, recorder.Code)
		}
		if tt.status == http.StatusPartialContent && (recorder.Body.String() != tt.body || recorder.Header().Get("Content-Range") != tt.contentRange) {
			t.Fatalf("%s body=%q range=%s", tt.rangeHeader, recorder.Body.String(), recorder.Header().Get("Content-Range"))
		}
	}
}

type advancingResponse struct {
	*httptest.ResponseRecorder
	advance func()
}

func (w *advancingResponse) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if w.advance != nil {
		advance := w.advance
		w.advance = nil
		advance()
	}
	return n, err
}

func TestLiveHTTPAbortsWhenReaderFallsOutOfWindow(t *testing.T) {
	s, _ := liveSpool(t, 8)
	_, _ = s.Write([]byte("0123"))
	stream := ServeSpool(output.Opening{}, s)
	recorder := &advancingResponse{ResponseRecorder: httptest.NewRecorder(), advance: func() { _, _ = s.Write([]byte("456789abcdef")) }}
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Fatalf("expired response did not abort: %v", got)
		}
		if recorder.Body.String() != "0123" {
			t.Fatalf("mixed expired bytes into response: %q", recorder.Body.String())
		}
	}()
	stream.handleStream(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example/stream", nil))
}

func TestLiveByteZeroRangeFollowsProduction(t *testing.T) {
	for _, initial := range []string{"", "first"} {
		t.Run("initial="+initial, func(t *testing.T) {
			spool, _ := liveSpool(t, 64)
			_, _ = spool.Write([]byte(initial))
			stream := ServeSpool(output.Opening{}, spool)
			done := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				stream.handleStream(w, r)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			req.Header.Set("Range", "bytes=0-")
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK || response.ContentLength != -1 || response.Header.Get("Content-Range") != "" {
				t.Fatalf("growing response: status=%d length=%d range=%q", response.StatusCode, response.ContentLength, response.Header.Get("Content-Range"))
			}
			prefix := make([]byte, len(initial))
			if _, err := io.ReadFull(response.Body, prefix); err != nil {
				t.Fatal(err)
			}
			if string(prefix) != initial {
				t.Fatalf("prefix=%q", prefix)
			}
			_, _ = spool.Write([]byte("later"))
			spool.CloseWrite(nil)
			rest, err := io.ReadAll(response.Body)
			if err != nil || string(rest) != "later" {
				t.Fatalf("rest=%q error=%v", rest, err)
			}
			<-done
		})
	}
}

func TestLiveByteZeroRangeCancellation(t *testing.T) {
	spool, _ := liveSpool(t, 64)
	stream := ServeSpool(output.Opening{}, spool)
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		stream.handleStream(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	req.Header.Set("Range", "bytes=0-")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	// Headers prove admission succeeded while the producer still has no bytes.
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request did not stop after cancellation")
	}
}

func TestCompletedLiveByteZeroRange(t *testing.T) {
	spool, _ := liveSpool(t, 64)
	_, _ = spool.Write([]byte("complete"))
	spool.CloseWrite(nil)
	stream := ServeSpool(output.Opening{}, spool)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example/stream", nil)
	req.Header.Set("Range", "bytes=0-")
	recorder := httptest.NewRecorder()
	stream.handleStream(recorder, req)
	if recorder.Code != http.StatusPartialContent || recorder.Body.String() != "complete" || recorder.Header().Get("Content-Range") != "bytes 0-7/8" || recorder.Header().Get("Content-Length") != "8" {
		t.Fatalf("completed response: status=%d headers=%v body=%q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}
