package mediaroute

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// TestAStreamWithNothingToServeIsHeldOpenThroughTheRoute: a device waiting on a read that runs behind finds its request unanswered, not ended.
func TestAStreamWithNothingToServeIsHeldOpenThroughTheRoute(t *testing.T) {
	sp, err := deliver.NewSpool(filepath.Join(t.TempDir(), "spool.ts"))
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, 188*8)
	if _, err := sp.Write(first); err != nil {
		t.Fatal(err)
	}
	deliveries := NewDeliveries(&url.URL{Scheme: "http", Host: "unused"}, "cast")
	srv, err := deliver.OpenSpooledStream(t.Context(), deliver.Opening{
		Format:        container.Format{ContentType: media.MPEGTS, Extension: ".ts"},
		Listeners:     deliveries,
		WriteDeadline: time.Minute,
	}, sp, make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close(); sp.CloseWrite(nil) })

	_, port, _ := net.SplitHostPort(srv.URL().Host)
	mux := http.NewServeMux()
	mux.Handle(Pattern, Handler(func(cast, p string) bool { return p == port }))
	front := httptest.NewTestServer(t, mux)

	resp, err := front.Client().Get(front.URL + "/media/cast/" + port + "/stream.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
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
