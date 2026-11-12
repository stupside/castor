package progressive

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/testhttp"
)

type loopback struct{}

func (loopback) Publish(ctx context.Context, h http.Handler) (output.Publication, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return testhttp.Publish(ctx, ln, h), nil
}

func TestStaticServesTheDocument(t *testing.T) {
	u, closeStatic, err := output.Static(t.Context(), loopback{}, "en.vtt", "text/vtt", "WEBVTT\n\n")
	if err != nil {
		t.Fatal(err)
	}
	defer closeStatic()
	resp, err := http.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "WEBVTT\n\n" || resp.Header.Get("Content-Type") != "text/vtt" {
		t.Errorf("served %q as %q", body, resp.Header.Get("Content-Type"))
	}
	for _, test := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodHead, u.Path, http.StatusOK},
		{http.MethodPost, u.Path, http.StatusMethodNotAllowed},
		{http.MethodGet, "/missing.vtt", http.StatusNotFound},
	} {
		at := *u
		at.Path = test.path
		req, err := http.NewRequestWithContext(t.Context(), test.method, at.String(), nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Errorf("%s %s: got %d, want %d", test.method, test.path, response.StatusCode, test.status)
		}
	}
}
