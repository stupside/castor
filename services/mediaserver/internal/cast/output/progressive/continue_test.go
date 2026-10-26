package progressive

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func finished(t *testing.T, data []byte) *storage.Spool {
	t.Helper()
	sp, err := storage.NewSpool(filepath.Join(t.TempDir(), "s.ts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sp.Close() })
	if _, err := sp.Write(data); err != nil {
		t.Fatal(err)
	}
	sp.CloseWrite(nil)
	return sp
}

func continuing(t *testing.T) (*Stream, *httptest.Server, []byte, []byte) {
	t.Helper()
	format, _ := container.For(media.MPEGTS)
	own, rest := bytes.Repeat([]byte("a"), 1<<20), bytes.Repeat([]byte("b"), 4096)
	stream := ServeSpool(output.Opening{Format: format}, finished(t, own))
	srv := httptest.NewServer(stream.Handler())
	t.Cleanup(srv.Close)
	return stream, srv, own, rest
}

func retrieve(t *testing.T, url, byteRange string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if byteRange != "" {
		req.Header.Set("Range", byteRange)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func TestAStreamCarriesOnFromAnotherBufferAfterTheCut(t *testing.T) {
	stream, srv, own, rest := continuing(t)
	const at = 600 << 10
	if !stream.Continue(at, finished(t, rest)) {
		t.Fatal("a stream nobody was written to refused to carry on")
	}
	_, body := retrieve(t, srv.URL+"/"+stream.Path(), "")
	if want := append(append([]byte{}, own[:at]...), rest...); !bytes.Equal(body, want) {
		t.Errorf("served %d bytes, want the first %d of the stream's own then the %d it carries on with", len(body), at, len(rest))
	}
}

func TestAClientStartingPastTheCutIsServedFromTheOtherBuffer(t *testing.T) {
	stream, srv, _, rest := continuing(t)
	const at = 600 << 10
	stream.Continue(at, finished(t, rest))
	resp, body := retrieve(t, srv.URL+"/"+stream.Path(), "bytes="+strconv.Itoa(at+100)+"-")
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(body, rest[100:]) {
		t.Errorf("a range past the cut got %d with %d bytes, want 206 and the last %d of the other buffer", resp.StatusCode, len(body), len(rest)-100)
	}
	if total := resp.Header.Get("Content-Range"); total != "bytes "+strconv.Itoa(at+100)+"-"+strconv.Itoa(at+len(rest)-1)+"/"+strconv.Itoa(at+len(rest)) {
		t.Errorf("Content-Range = %q, want the two buffers' length", total)
	}
}

func TestAStreamAlreadyServedPastTheCutDoesNotCarryOn(t *testing.T) {
	stream, srv, _, rest := continuing(t)
	retrieve(t, srv.URL+"/"+stream.Path(), "")
	if stream.Continue(600<<10, finished(t, rest)) {
		t.Error("a stream carried on from a cut a client had been served past")
	}
}

func TestAStreamCarriesOnOnlyOnce(t *testing.T) {
	stream, _, _, rest := continuing(t)
	if !stream.Continue(600<<10, finished(t, rest)) || stream.Continue(700<<10, finished(t, rest)) {
		t.Error("a stream carried on twice")
	}
}
