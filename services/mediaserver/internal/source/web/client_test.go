package web

import (
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/sourcetest"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

func TestFetchReportsWhatTheOriginSaid(t *testing.T) {
	const body = "#EXTM3U\n#EXT-X-ENDLIST\n"
	origin := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hls/master.m3u8":
			http.Redirect(w, r, "/edge/a/b/master.m3u8", http.StatusFound)
		case "/edge/a/b/master.m3u8":
			if got := r.Header.Get("Referer"); got != "https://player.example/" {
				t.Errorf("Referer = %q, want the captured header replayed", got)
			}
			_, _ = w.Write([]byte(body))
		case "/spent.m3u8":
			http.Error(w, "expired signature", http.StatusForbidden)
		case "/film.m3u8":
			_, _ = w.Write(make([]byte, source.DocumentLimit+1))
		}
	}))
	client := reaching(origin, 5*time.Second)

	// Relative URIs resolve against where the document was served from, not the URL asked for.
	got, from, status, err := client.Fetch(t.Context(), sourcetest.URL(t, origin.URL+"/hls/master.m3u8"), http.Header{"Referer": {"https://player.example/"}})
	if err != nil || status != http.StatusOK || got != body {
		t.Fatalf("Fetch = (%q, %d, %v), want the body with 200", got, status, err)
	}
	if from.Path != "/edge/a/b/master.m3u8" {
		t.Errorf("from = %q, want the redirected path", from.Path)
	}
	if _, _, status, err := client.Fetch(t.Context(), sourcetest.URL(t, origin.URL+"/spent.m3u8"), nil); err == nil || status != http.StatusForbidden {
		t.Errorf("Fetch = (%d, %v), want a 403 error", status, err)
	}
	if _, _, _, err := client.Fetch(t.Context(), sourcetest.URL(t, origin.URL+"/film.m3u8"), nil); err == nil {
		t.Error("a body past the document limit was read whole")
	}
}

func TestAFreshSessionCookieReplacesTheCopyTakenEarlier(t *testing.T) {
	var got []string
	origin := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Values("Cookie")
		http.SetCookie(w, &http.Cookie{Name: "token", Value: "fresh", Path: "/"})
		_, _ = w.Write([]byte("#EXTM3U\n"))
	}))
	client := reaching(origin, 5*time.Second)
	u := sourcetest.URL(t, origin.URL+"/live.m3u8")
	if _, _, _, err := client.Fetch(t.Context(), u, nil); err != nil {
		t.Fatal(err)
	}
	// The input still carries the value the session held at resolve, next to the page's own cookie.
	if _, _, _, err := client.Fetch(t.Context(), u, http.Header{"Cookie": {"token=stale; consent=yes"}}); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(got, "; "); strings.Contains(joined, "stale") || !strings.Contains(joined, "token=fresh") || !strings.Contains(joined, "consent=yes") {
		t.Errorf("Cookie = %q, want the fresh token once and the page's own cookie kept", joined)
	}
}

func TestAReplayCarriesTheSessionCookieOverThePagesCopy(t *testing.T) {
	origin := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "token", Value: "fresh", Path: "/"})
		_, _ = w.Write([]byte("#EXTM3U\n"))
	}))
	client := reaching(origin, 5*time.Second)
	u := sourcetest.URL(t, origin.URL+"/live.m3u8")
	page := http.Header{"Cookie": {"consent=yes; token=stale"}, "Referer": {"https://player.example/"}}
	if got := client.Replay(u, page); !maps.EqualFunc(got, page, slices.Equal) {
		t.Errorf("Replay = %v before the origin set a session, want the page's headers as they were", got)
	}
	if _, _, _, err := client.Fetch(t.Context(), u, nil); err != nil {
		t.Fatal(err)
	}
	got := client.Replay(u, page)
	if c := got.Get("Cookie"); c != "consent=yes; token=fresh" || got.Get("Referer") != "https://player.example/" {
		t.Errorf("Replay = %v, want the page's own cookie, then the session's fresh token in place of its copy", got)
	}
	if page.Get("Cookie") != "consent=yes; token=stale" {
		t.Error("Replay changed the headers it was handed")
	}
}

func TestReplayPreservesCookiesFromEveryCapturedHeader(t *testing.T) {
	client := New(time.Second).(*client)
	u := sourcetest.URL(t, "https://origin.example/film.m3u8")
	client.http.Jar.SetCookies(u, []*http.Cookie{{Name: "token", Value: "fresh", Path: "/"}})
	got := client.Replay(u, http.Header{"Cookie": {"token=stale", "consent=yes"}})
	if got.Get("Cookie") != "consent=yes; token=fresh" {
		t.Errorf("Cookie = %q, want both the captured consent and refreshed token", got.Get("Cookie"))
	}
}

func TestMediaRangeRejectsBytesFromADifferentOffset(t *testing.T) {
	origin := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-3/8")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("init"))
	}))
	body, err := reaching(origin, time.Second).Read(t.Context(), sourcetest.URL(t, origin.URL), nil, timeline.Range{Offset: 4, Length: 4})
	if body != nil {
		_ = body.Close()
	}
	if err == nil {
		t.Fatal("read init bytes as the requested media fragment at offset 4")
	}
}

func TestAnOriginIgnoringRangeMustStillDeliverTheWholeRequestedFragment(t *testing.T) {
	origin := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("initshort"))
	}))
	body, err := reaching(origin, time.Second).Read(t.Context(), sourcetest.URL(t, origin.URL), nil, timeline.Range{Offset: 4, Length: 8})
	if err == nil {
		defer body.Close()
		_, err = io.ReadAll(body)
	}
	if err == nil {
		t.Fatal("a five-byte response succeeded as the requested eight-byte fragment")
	}
}

// reaching is a client whose reads go over origin's in-memory network.
func reaching(origin *httptest.Server, timeout time.Duration) *client {
	c := New(timeout).(*client)
	c.http.Transport = origin.Client().Transport
	return c
}
