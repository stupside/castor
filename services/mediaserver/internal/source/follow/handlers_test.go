package follow

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

func TestASegmentCastorNoLongerHoldsIsGone(t *testing.T) {
	server := serving(t, fragmented("avc1"), labelled)
	fetch(t, server.URL("primary").String())
	if status, _ := fetch(t, server.base.JoinPath("primary", "9.ts").String()); status != http.StatusNotFound {
		t.Errorf("a segment the ledger never held answered %d, want 404", status)
	}
}

func TestTheOriginsRefusalIsRelayedAndAnythingElseIsABadGateway(t *testing.T) {
	for status, want := range map[int]int{http.StatusForbidden: http.StatusForbidden, http.StatusOK: http.StatusBadGateway} {
		m := fragmented("av01")
		m.fail = &timeline.Failure{Status: status, Err: errors.New("origin")}
		server := serving(t, m, labelled)
		fetch(t, server.URL("primary").String())
		if got, _ := fetch(t, server.base.JoinPath("primary", "1").String()); got != want {
			t.Errorf("an origin failing with %d answered %d, want %d", status, got, want)
		}
	}
}

// An empty 200 would read as an empty segment, which the reader never retries.
func TestABodyThatBreaksBeforeItsFirstByteIsABadGateway(t *testing.T) {
	m := fragmented("av01")
	m.breaks = true
	server := serving(t, m, labelled)
	fetch(t, server.URL("primary").String())
	if got, _ := fetch(t, server.base.JoinPath("primary", "1").String()); got != http.StatusBadGateway {
		t.Errorf("a segment whose body broke before any byte answered %d, want 502", got)
	}
}

type truncatedSegment struct{ *stored }

func (s truncatedSegment) Read(ctx context.Context, uri string, r timeline.Range) (io.ReadCloser, error) {
	if strings.HasSuffix(uri, ".m4s") {
		return io.NopCloser(io.MultiReader(strings.NewReader("partial fragment"), iotest.ErrReader(io.ErrUnexpectedEOF))), nil
	}
	return s.stored.Read(ctx, uri, r)
}

func TestABodyThatBreaksAfterItsFirstByteDoesNotSucceedAsAPartialSegment(t *testing.T) {
	server := serving(t, truncatedSegment{fragmented("av01")}, nil)
	fetch(t, server.URL("primary").String())
	resp, err := http.Get(server.base.JoinPath("primary", "1").String())
	if err == nil {
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
	}
	if err == nil {
		t.Fatal("a truncated fragment was answered as a complete HTTP response")
	}
}

func TestAResumeOrAHeadNeverReachesTheOrigin(t *testing.T) {
	m := fragmented("av01")
	server := serving(t, m, labelled)
	fetch(t, server.URL("primary").String())
	req, _ := http.NewRequest(http.MethodGet, server.base.JoinPath("primary", "1").String(), nil)
	req.Header.Set("Range", "bytes=4-")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("a resume partway in answered %d, want 416", resp.StatusCode)
	}
	head, err := http.Head(server.base.JoinPath("primary", "1").String())
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	if head.StatusCode != http.StatusOK || m.reads["https://origin.example/1.m4s"] != 0 {
		t.Errorf("a HEAD answered %d after %d origin reads, want 200 and none", head.StatusCode, m.reads["https://origin.example/1.m4s"])
	}
}

func TestAKeyCastorCannotApplyIsRelayedForTheReader(t *testing.T) {
	m := fragmented("av01")
	m.resources["https://origin.example/skey"] = []byte("key bytes")
	for i := range m.window.Segments {
		m.window.Segments[i].Key = timeline.Key{Method: "SAMPLE-AES", URI: "https://origin.example/skey", IV: "0x01"}
	}
	server := serving(t, m, labelled)
	_, playlist := fetch(t, server.URL("primary").String())
	if !strings.Contains(playlist, `#EXT-X-KEY:METHOD=SAMPLE-AES,URI="primary/key/0",IV=0x01`) {
		t.Fatalf("the key is not relayed through castor:\n%s", playlist)
	}
	if _, key := fetch(t, server.base.JoinPath("primary", "key", "0").String()); key != "key bytes" {
		t.Errorf("the relayed key = %q", key)
	}
}
