package transport

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestBearerDoesNotFollowRedirectsToAnotherServer(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(r.Header.Get("Authorization") != "")
	}))
	defer other.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("the configured server did not receive its bearer token")
		}
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer first.Close()
	resp, err := Bearer("secret").Get(first.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if leaked.Load() {
		t.Fatal("a redirect forwarded the bearer token to another server")
	}
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("redirect response = %d, want the original redirect", resp.StatusCode)
	}
}
