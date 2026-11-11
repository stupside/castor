package forward

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestTheDeviceSeeksWhatTheOriginServes(t *testing.T) {
	file := bytes.Repeat([]byte("castor"), 1000)
	var asked http.Header
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.Header.Clone()
		switch r.URL.Path {
		case "/movie":
			w.Header().Set("Content-Type", "application/octet-stream")
			http.ServeContent(w, r, "movie", time.Time{}, bytes.NewReader(file))
		case "/broken":
			http.Error(w, "upstream down", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()
	at := func(path string) *url.URL { u, _ := url.Parse(origin.URL + path); return u }
	served := map[string]string{"Accept-Ranges": "none", "transferMode.dlna.org": "Streaming"}
	headers := http.Header{"Referer": {"https://player.example/"}}
	device := httptest.NewServer(Handler(origin.Client(), at("/movie"), headers, "video/x-matroska", served))
	defer device.Close()

	req, _ := http.NewRequest(http.MethodGet, device.URL, nil)
	req.Header.Set("Range", "bytes=6-11")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "castor" {
		t.Fatalf("status %d body %q, want 206 with the origin's bytes 6-11", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes 6-11/6000" {
		t.Errorf("Content-Range = %q, want the origin's", got)
	}
	if resp.Header.Get("Accept-Ranges") != "bytes" || resp.Header.Get("transferMode.dlna.org") != "Streaming" {
		t.Errorf("the origin's range support must replace the served default, beside the device's own headers: %v", resp.Header)
	}
	if resp.Header.Get("Content-Type") != "video/x-matroska" {
		t.Errorf("Content-Type = %q, want the container the device was told", resp.Header.Get("Content-Type"))
	}
	if asked.Get("Referer") != "https://player.example/" || asked.Get("Range") != "bytes=6-11" {
		t.Errorf("the origin must get the source's headers and the device's range, got %v", asked)
	}

	head, err := http.Head(device.URL)
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	if head.StatusCode != http.StatusOK || head.ContentLength != int64(len(file)) {
		t.Errorf("HEAD status %d length %d, want the whole file's", head.StatusCode, head.ContentLength)
	}

	for path, want := range map[string]int{"/missing": http.StatusNotFound, "/broken": http.StatusBadGateway} {
		failing := httptest.NewServer(Handler(origin.Client(), at(path), nil, "video/mp4", nil))
		resp, err := http.Get(failing.URL)
		failing.Close()
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("origin path %s answered %d to the device, want %d", path, resp.StatusCode, want)
		}
	}
}

func TestNameKeepsTheExtensionRenderersSniff(t *testing.T) {
	if Name("video/x-matroska") != "source.mkv" || Name("video/mp4") != "source.mp4" || Name("application/octet-stream") != "source" {
		t.Fatal("unexpected forwarded source names")
	}
}
