package progressive

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// wrappedResponse exposes only the standard ResponseWriter and Unwrap contract.
type wrappedResponse struct{ http.ResponseWriter }

func (w wrappedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type failedFlush struct{ *httptest.ResponseRecorder }

func (failedFlush) FlushError() error { return io.ErrClosedPipe }

func TestStreamFlushesThroughResponseWrappersAndStopsOnFlushFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("flush_failure=%t", fail), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			var writer http.ResponseWriter = recorder
			if fail {
				writer = failedFlush{recorder}
			}
			wrapped := wrappedResponse{writer}
			s := &Stream{}
			sv := served{code: http.StatusOK, end: -1, total: -1}
			if err := s.respond(wrapped, sv); (err != nil) != fail {
				t.Fatalf("flushing headers: %v", err)
			}
			complete := s.send(t.Context(), wrapped, httptest.NewRequest(http.MethodGet, "/", nil), sv, strings.NewReader("media"), 0)
			if complete == fail {
				t.Fatalf("completed=%t despite flush failure=%t", complete, fail)
			}
			held, _ := s.Handed()
			if fail && held != 0 {
				t.Fatalf("credited %d bytes after a failed flush", held)
			}
			if !fail && (!recorder.Flushed || held != 5) {
				t.Fatalf("wrapped response: flushed=%t, handed=%d", recorder.Flushed, held)
			}
		})
	}
}

// Handed is the most one client took: a HEAD takes nothing, and a later partial client does not add to it.
func TestHandedIsTheMostAnyOneClientTook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const size = 8 << 20
		srv := spooled(t, testOpening{}, payload(size))

		head(t, srv)
		if handed, last := srv.Handed(); handed != 0 || !last.IsZero() {
			t.Fatalf("a HEAD probe was credited with %d bytes at %v", handed, last)
		}

		// Read to EOF: the in-memory pipe has no socket buffer to absorb the chunked trailer.
		if whole, err := io.ReadAll(get(t, srv, "").Body); err != nil || len(whole) != size {
			t.Fatalf("a whole client took %d bytes (%v), want %d", len(whole), err, size)
		}
		partial := get(t, srv, "")
		read(t, partial, 1)
		_ = partial.Body.Close()
		settled(t, srv)
		if handed, last := srv.Handed(); handed != size || last.IsZero() {
			t.Errorf("handed = %d at %v, want the %d bytes one client took", handed, last, size)
		}
	})
}

// A resumed client is served its range and credited with the prefix it already had.
func TestARangeResumesFromTheByteAsked(t *testing.T) {
	want := payload(1 << 20)
	total := int64(len(want))
	from := total / 2
	for _, tt := range []struct {
		name     string
		asked    string
		to       int64
		finishes bool
	}{
		{"open-ended", fmt.Sprintf("bytes=%d-", from), total - 1, true},
		{"bounded to the last byte", fmt.Sprintf("bytes=%d-%d", from, total-1), total - 1, true},
		{"bounded short of the end", fmt.Sprintf("bytes=%d-%d", from, total-1024), total - 1024, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := spooled(t, testOpening{}, want)
				resp := get(t, srv, tt.asked)
				if resp.StatusCode != http.StatusPartialContent {
					t.Fatalf("status = %d, want 206", resp.StatusCode)
				}
				if got, stated := resp.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-%d/%d", from, tt.to, total); got != stated {
					t.Errorf("Content-Range = %q, want %q", got, stated)
				}
				got, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want[from:tt.to+1]) {
					t.Errorf("handed %d bytes, want exactly bytes %d-%d", len(got), from, tt.to)
				}
				if !tt.finishes {
					return
				}
				settled(t, srv)
				if handed, _ := srv.Handed(); handed != total {
					t.Errorf("handed = %d, want the whole %d the resumed client holds", handed, total)
				}
			})
		})
	}
}

func TestAResumePastTheEndIs416(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		want := payload(4096)
		resp := get(t, spooled(t, testOpening{}, want), fmt.Sprintf("bytes=%d-", len(want)))
		if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("status = %d, want 416", resp.StatusCode)
		}
		if got, stated := resp.Header.Get("Content-Range"), fmt.Sprintf("bytes */%d", len(want)); got != stated {
			t.Errorf("Content-Range = %q, want %q", got, stated)
		}
	})
}

// A range nobody can honour is replayed from byte 0 rather than answered with an invented length.
func TestARefusedRangeReplaysFromByteZero(t *testing.T) {
	body := payload(64 << 10)
	for _, tt := range []struct {
		name string
		srv  func(t *testing.T) *testStream
	}{
		{"a delivery declaring Accept-Ranges: none", func(t *testing.T) *testStream {
			return spooled(t, testOpening{Headers: map[string]string{"Accept-Ranges": "none"}}, body)
		}},
		{"a producer still running", func(t *testing.T) *testStream { return producing(t, body) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				resp := get(t, tt.srv(t), fmt.Sprintf("bytes=%d-", len(body)/2))
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200", resp.StatusCode)
				}
				if got := read(t, resp, len(body)); !bytes.Equal(got, body) {
					t.Error("the refused client was not served from byte 0")
				}
			})
		})
	}
}

// A client that stops reading, as a device its viewer paused does, is held however long, and takes the rest once it reads again.
func TestAClientThatStopsReadingIsHeldUntilItReadsAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := payload(8 << 20)
		srv := spooled(t, testOpening{}, body)
		resp := get(t, srv, "")
		first := read(t, resp, 1)
		time.Sleep(2 * time.Hour)
		rest, err := io.ReadAll(resp.Body)
		if err != nil || !bytes.Equal(append(first, rest...), body) {
			t.Fatalf("after two hours without reading the client took %d more bytes (%v), want the rest of the %d", len(rest), err, len(body))
		}
	})
}

// A spool another writes is replayed from byte 0, resumed once its writer is done, and let go of without waiting for that writer.
func TestASpoolAnotherWritesIsServedWithoutBeingOwned(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := payload(1 << 20)
		sp, err := storage.NewSpool(filepath.Join(t.TempDir(), "spool.ts"))
		if err != nil {
			t.Fatal(err)
		}
		half := len(body) / 2
		if _, err := sp.Write(body[:half]); err != nil {
			t.Fatal(err)
		}
		drained := make(chan struct{})
		devices := newLAN(t)
		srv, err := serveSpoolFixture(t.Context(), testOpening{
			Format:    container.Format{ContentType: media.MPEGTS, Extension: ".ts"},
			Publisher: devices,
		}, sp, drained)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = srv.Close() })

		whole := get(t, srv, "")
		if got := read(t, whole, half); !bytes.Equal(got, body[:half]) {
			t.Error("a client of a spool still being written was not served it from byte 0")
		}
		if resp := get(t, srv, fmt.Sprintf("bytes=%d-", half)); resp.StatusCode != http.StatusOK {
			t.Errorf("a range asked while the writer runs answered %d, want the 200 replay", resp.StatusCode)
		}

		if _, err := sp.Write(body[half:]); err != nil {
			t.Fatal(err)
		}
		sp.CloseWrite(nil)
		close(drained)
		if got, err := io.ReadAll(whole.Body); err != nil || !bytes.Equal(got, body[half:]) {
			t.Errorf("the replaying client got %d more bytes (%v), want the rest the writer added", len(got), err)
		}
		resumed := get(t, srv, fmt.Sprintf("bytes=%d-", half))
		if resumed.StatusCode != http.StatusPartialContent {
			t.Fatalf("a range asked once the writer is done answered %d, want 206", resumed.StatusCode)
		}
		if got, _ := io.ReadAll(resumed.Body); !bytes.Equal(got, body[half:]) {
			t.Errorf("the resumed client got %d bytes, want the %d from the byte it asked", len(got), len(body)-half)
		}

		running, err := storage.NewSpool(filepath.Join(t.TempDir(), "running.ts"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { running.CloseWrite(nil) })
		unowned, err := serveSpoolFixture(t.Context(), testOpening{Publisher: devices}, running, make(chan struct{}))
		if err != nil {
			t.Fatal(err)
		}
		closed := make(chan error, 1)
		go func() { closed <- unowned.Close() }()
		synctest.Wait()
		select {
		case <-closed:
		default:
			t.Fatal("Close waited for a writer the server does not own")
		}
	})
}

func payload(size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte(i % 251)
	}
	return out
}

// spooled starts a server and returns once the whole body is in the spool.
func spooled(t *testing.T, o testOpening, body []byte) *testStream {
	t.Helper()
	o.Publisher = newLAN(t)
	o.Format = container.Format{ContentType: "video/mp4", Extension: ".mp4"}
	o.IdleGrace = cmp.Or(o.IdleGrace, 30*time.Second)
	srv, err := serveStreamFixture(t.Context(), o, t.TempDir(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	<-srv.Produced()
	return srv
}

// producing serves a head the producer has written while it is still running.
func producing(t *testing.T, head []byte) *testStream {
	t.Helper()
	pr, pw := io.Pipe()
	srv, err := serveStreamFixture(t.Context(), testOpening{
		Format:    container.Format{ContentType: "video/mp4", Extension: ".mp4"},
		Publisher: newLAN(t),
	}, t.TempDir(), pr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pw.Close()
		_ = srv.Close()
	})
	if _, err := pw.Write(head); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	if landed, _ := srv.spool.Snapshot(); landed < int64(len(head)) {
		t.Fatal("the head never reached the spool")
	}
	return srv
}

// settled waits for every client to be accounted for.
func settled(t *testing.T, srv *testStream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := srv.Wait(ctx); err != nil {
		t.Fatalf("Wait = %v, want nil once every client is gone", err)
	}
}

func get(t *testing.T, srv *testStream, byteRange string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if byteRange != "" {
		req.Header.Set("Range", byteRange)
	}
	resp, err := device(srv.o).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func read(t *testing.T, resp *http.Response, n int) []byte {
	t.Helper()
	got := make([]byte, n)
	if _, err := io.ReadFull(resp.Body, got); err != nil {
		t.Fatalf("reading the served stream: %v", err)
	}
	return got
}

func head(t *testing.T, srv *testStream) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodHead, srv.URL().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := device(srv.o).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
