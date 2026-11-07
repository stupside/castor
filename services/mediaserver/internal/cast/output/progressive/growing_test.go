package progressive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
)

func TestGrowingFiniteRangesResumeAtTheRequestedByteWithoutClaimingAFinalLength(t *testing.T) {
	for _, tt := range []struct {
		asked, contentRange string
		first, last         int
	}{
		{"bytes=3-", "bytes 3-9/*", 3, 10},
		{"bytes=3-5", "bytes 3-5/*", 3, 6},
		{"bytes=0-3", "bytes 0-3/*", 0, 4},
	} {
		t.Run(tt.asked, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				body := []byte("0123456789")
				srv := producing(t, body)
				resp := get(t, srv, tt.asked)
				if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Range") != tt.contentRange {
					t.Fatalf("growing resume: status=%d range=%q; want 206 %q", resp.StatusCode, resp.Header.Get("Content-Range"), tt.contentRange)
				}
				got, err := io.ReadAll(resp.Body)
				if err != nil || !bytes.Equal(got, body[tt.first:tt.last]) {
					t.Fatalf("growing resume = %q, %v; want %q", got, err, body[tt.first:tt.last])
				}
			})
		})
	}
}

func TestGrowingFiniteByteZeroRangeStillFollowsProduction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sp, err := storage.NewSpool(filepath.Join(t.TempDir(), "growing.mp4"))
		if err != nil {
			t.Fatal(err)
		}
		defer sp.Close()
		_, _ = sp.Write([]byte("first"))
		srv, err := serveSpoolFixture(t.Context(), testOpening{Publisher: newLAN(t)}, sp, make(chan struct{}))
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()
		resp := get(t, srv, "bytes=0-")
		if resp.StatusCode != http.StatusOK || resp.ContentLength != -1 || resp.Header.Get("Content-Range") != "" {
			t.Fatalf("initial growing response: status=%d length=%d range=%q", resp.StatusCode, resp.ContentLength, resp.Header.Get("Content-Range"))
		}
		if got := read(t, resp, 5); string(got) != "first" {
			t.Fatalf("initial bytes=%q", got)
		}
		result := make(chan []byte, 1)
		go func() { rest, _ := io.ReadAll(resp.Body); result <- rest }()
		synctest.Wait()
		select {
		case <-result:
			t.Fatal("initial byte-zero request ended at the current prefix")
		default:
		}
		_, _ = sp.Write([]byte("later"))
		sp.CloseWrite(nil)
		if got := <-result; string(got) != "later" {
			t.Fatalf("later bytes=%q", got)
		}
	})
}

func TestGrowingFiniteRangeWaitsForItsOffsetInsteadOfReplayingTheBeginning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sp, err := storage.NewSpool(filepath.Join(t.TempDir(), "growing.mp4"))
		if err != nil {
			t.Fatal(err)
		}
		defer sp.Close()
		_, _ = sp.Write([]byte("0123"))
		srv, err := serveSpoolFixture(t.Context(), testOpening{Publisher: newLAN(t)}, sp, make(chan struct{}))
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()
		response := make(chan *http.Response, 1)
		go func() { response <- get(t, srv, "bytes=6-") }()
		synctest.Wait()
		select {
		case resp := <-response:
			t.Fatalf("future offset answered early with %d", resp.StatusCode)
		default:
		}
		_, _ = sp.Write([]byte("456789"))
		resp := <-response
		if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Range") != "bytes 6-9/*" {
			t.Fatalf("future resume: status=%d range=%q", resp.StatusCode, resp.Header.Get("Content-Range"))
		}
		if got, err := io.ReadAll(resp.Body); err != nil || string(got) != "6789" {
			t.Fatalf("future resume=%q, %v", got, err)
		}
		sp.CloseWrite(nil)
	})
}

func TestGrowingFiniteFutureRangeEndsOnCancellationOrTheRealEnd(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel bool
	}{{"client cancelled", true}, {"producer completed before offset", false}} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sp, err := storage.NewSpool(filepath.Join(t.TempDir(), "growing.mp4"))
				if err != nil {
					t.Fatal(err)
				}
				defer sp.Close()
				_, _ = sp.Write([]byte("0123"))
				srv, err := serveSpoolFixture(t.Context(), testOpening{Publisher: newLAN(t)}, sp, make(chan struct{}))
				if err != nil {
					t.Fatal(err)
				}
				defer srv.Close()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL().String(), nil)
				req.Header.Set("Range", "bytes=99-")
				type answer struct {
					response *http.Response
					err      error
				}
				result := make(chan answer, 1)
				go func() { resp, err := device(srv.o).Do(req); result <- answer{resp, err} }()
				synctest.Wait()
				if tc.cancel {
					cancel()
				} else {
					sp.CloseWrite(nil)
				}
				got := <-result
				if tc.cancel {
					if !errors.Is(got.err, context.Canceled) {
						t.Fatalf("cancelled range returned %v", got.err)
					}
				} else {
					if got.err != nil {
						t.Fatal(got.err)
					}
					defer got.response.Body.Close()
					if got.response.StatusCode != http.StatusRequestedRangeNotSatisfiable || got.response.Header.Get("Content-Range") != "bytes */4" {
						t.Fatalf("completed range: status=%d range=%q", got.response.StatusCode, got.response.Header.Get("Content-Range"))
					}
				}
				sp.CloseWrite(nil)
			})
		})
	}
}
