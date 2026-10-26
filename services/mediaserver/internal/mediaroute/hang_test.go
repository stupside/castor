package mediaroute

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/progressive"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// TestAStreamWithNothingToServeIsHeldOpenThroughTheRoute: a device waiting on a read that runs behind finds its request unanswered, not ended.
func TestAStreamWithNothingToServeIsHeldOpenThroughTheRoute(t *testing.T) {
	sp, err := storage.NewSpool(filepath.Join(t.TempDir(), "spool.ts"))
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, 188*8)
	if _, err := sp.Write(first); err != nil {
		t.Fatal(err)
	}
	publications := NewPublisher(&url.URL{Scheme: "http", Host: "unused"})
	srv := progressive.ServeSpool(output.Opening{
		Format: container.Format{ContentType: media.MPEGTS, Extension: ".ts"},
	}, sp)
	pub, err := publications.Publish(t.Context(), srv.Handler())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close(); sp.CloseWrite(nil); _ = sp.Close() })

	front := httptest.NewTestServer(t, publications)

	resp, err := front.Client().Get(front.URL + pub.URL(srv.Path()).Path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}
	if _, err := io.ReadFull(resp.Body, make([]byte, len(first))); err != nil {
		t.Fatal(err)
	}

	// Nothing in castor ends a request on a dry spool, however long: a device that leaves does so on its own.
	dry := 3 * time.Second
	got := make(chan int, 1)
	go func() {
		n, _ := io.ReadFull(resp.Body, make([]byte, 188))
		got <- n
	}()
	select {
	case n := <-got:
		t.Fatalf("the request was answered with %d bytes while there was nothing to serve", n)
	case <-time.After(dry):
	}
	if _, err := sp.Write(make([]byte, 188)); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-got:
		if n != 188 {
			t.Errorf("the next %d bytes arrived, want 188", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the connection did not carry data written after the dry spell")
	}
}
