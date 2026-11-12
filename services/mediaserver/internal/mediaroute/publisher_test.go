package mediaroute

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestPublicationLivesUntilCloseAndPreservesCastContext(t *testing.T) {
	type key struct{}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), key{}, "cast-value"))
	d := NewPublisher(&url.URL{Scheme: "http", Host: "media.test"})
	pub, err := d.Publish(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil || r.Context().Value(key{}) != "cast-value" {
			t.Error("publication lost its serving lifetime or cast context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()
	cancel()
	response := httptest.NewRecorder()
	d.ServeHTTP(response, httptest.NewRequest(http.MethodGet, pub.URL("stream.ts").String(), nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("publication ended before Close: %d", response.Code)
	}
}

func TestCloseInterruptsWritesToADeviceThatStoppedReading(t *testing.T) {
	for _, protocol := range []string{"http1", "http2"} {
		t.Run(protocol, func(t *testing.T) {
			d := NewPublisher(&url.URL{Scheme: "https", Host: "media.test"})
			pub, err := d.Publish(t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if protocol == "http2" && r.ProtoMajor != 2 {
					t.Error("request did not use HTTP/2")
				}
				block := make([]byte, 32*1024)
				for {
					if _, err := w.Write(block); err != nil {
						return
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						return
					}
				}
			}))
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(d)
			server.EnableHTTP2 = protocol == "http2"
			server.StartTLS()
			defer server.Close()
			response, err := server.Client().Get(server.URL + pub.URL("stream.ts").Path)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			// Leave the body unread. The handler only exits when writing fails.
			closed := make(chan struct{})
			go func() { _ = pub.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("Close left a response writer blocked")
			}
		})
	}
}

func TestPublishedDeliveriesAreIndependentAndRemovedOnClose(t *testing.T) {
	d := NewPublisher(&url.URL{Scheme: "http", Host: "media.test"})
	pub, err := d.Publish(t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/segment.ts" {
			t.Errorf("handler received path %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, "media bytes")
	}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.Publish(t.Context(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "second delivery")
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	server := httptest.NewServer(d)
	defer server.Close()
	get := func(path string) int {
		t.Helper()
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	path := pub.URL("segment.ts").Path
	secondPath := second.URL("segment.ts").Path
	if path == secondPath {
		t.Fatal("simultaneous deliveries share a URL")
	}
	if code := get(path); code != http.StatusOK {
		t.Fatalf("published delivery: %d", code)
	}
	if code := get("/media/unknown/segment.ts"); code != http.StatusNotFound {
		t.Fatalf("an unpublished URL reached a delivery: %d", code)
	}
	if err := pub.Close(); err != nil {
		t.Fatal(err)
	}
	if code := get(path); code != http.StatusNotFound {
		t.Fatalf("closed delivery still served: %d", code)
	}
	if code := get(secondPath); code != http.StatusOK {
		t.Fatalf("closing one delivery closed another: %d", code)
	}
}

func TestClosingPublicationCancelsAnActiveRequest(t *testing.T) {
	d := NewPublisher(&url.URL{Scheme: "http", Host: "media.test"})
	entered, left := make(chan struct{}), make(chan struct{})
	pub, err := d.Publish(t.Context(), http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(left)
	}))
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() {
		d.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, pub.URL("segment.ts").String(), nil))
		close(finished)
	}()
	<-entered
	closed := make(chan struct{})
	go func() { _ = pub.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("closing the delivery did not cancel its active request")
	}
	<-left
	<-finished
}

func TestEveryPublishedResponseMayBeReadByAWebReceiver(t *testing.T) {
	d := NewPublisher(&url.URL{Scheme: "http", Host: "media.test"})
	pub, err := d.Publish(t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "#EXTM3U\n")
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()
	response := httptest.NewRecorder()
	d.ServeHTTP(response, httptest.NewRequest(http.MethodGet, pub.URL("stream.m3u8").String(), nil))
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
}
