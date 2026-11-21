package scrapingclient

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAScrapingServerThatRefusedTheTokenIsNamedWithItsConfigurationKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"unauthenticated","message":"this server asks for its bearer token"}`))
	}))
	defer server.Close()
	_, err := New(server.Client(), server.URL).ResolvePages(t.Context(), []string{"https://site.example/watch"})
	if err == nil || !strings.Contains(err.Error(), "the scraping server refused this server (check servers.scraping.token)") {
		t.Errorf("a refused call was reported %v, want the scraping server and its token named", err)
	}
}
