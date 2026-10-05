package extract

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// testCollector reads bodies only through noteDocument; the browser hands over none.
func testCollector(t *testing.T) *collector {
	t.Helper()
	unreadable := func(network.RequestID) ([]byte, error) { return nil, errors.New("no body") }
	return newCollector(t.Context(), unreadable, time.Second, time.Second, time.Second)
}

func urls(entries []*castorv1.StreamCandidate) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.GetStream().GetUrl()
	}
	return out
}

const (
	masterDocument = "#EXTM3U\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080\n" +
		"v/1080.m3u8\n"
	mediaDocument = "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6.000,\nseg_00001.m4s\n#EXT-X-ENDLIST\n"
)

func captureWithBody(c *collector, u string, reqID network.RequestID, body string) {
	c.addByURL(u, reqID)
	c.noteDocument(reqID, body)
}

// A link is captured by its name only when a format says the name is a segmented manifest's.
func TestALinkIsCapturedByNameOnlyAsAManifest(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"https://cdn.example/hls/index.m3u8?token=x", true},
		{"https://cdn.example/dash/stream.mpd?sig=abc", true},
		{"https://embed.example/playlist/abc", false},
		{"https://cdn.example/movie.mp4", false},
	} {
		c := testCollector(t)
		c.addByURL(tc.raw, "req-1")
		if got := c.hasHits(); got != tc.want {
			t.Errorf("addByURL(%q) captured = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// Extraction ranks nothing; the ladder comes from each body, never from the name.
func TestCapturesKeepTheOrderTheyWereFoundIn(t *testing.T) {
	c := testCollector(t)
	captureWithBody(c, "https://cdn.example/hls/master.m3u8", "req-named", mediaDocument)
	captureWithBody(c, "https://cdn.example/hls/index.m3u8", "req-real", masterDocument)
	c.addByURL("https://cdn.example/other/chunklist.m3u8", "req-unread")
	c.addByMIME("https://cdn.example/dash/manifest", "req-mpd", "application/dash+xml")

	want := []string{
		"https://cdn.example/hls/master.m3u8",
		"https://cdn.example/hls/index.m3u8",
		"https://cdn.example/other/chunklist.m3u8",
		"https://cdn.example/dash/manifest",
	}
	entries := c.entries()
	if got := urls(entries); !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want capture order %v", got, want)
	}
	for i, ladder := range []castorv1.Ladder{castorv1.Ladder_LADDER_SOLE, castorv1.Ladder_LADDER_MULTIVARIANT, castorv1.Ladder_LADDER_UNSPECIFIED, castorv1.Ladder_LADDER_UNSPECIFIED} {
		if entries[i].Ladder != ladder {
			t.Errorf("%s ladder = %v, want %v", entries[i].GetStream().GetUrl(), entries[i].Ladder, ladder)
		}
	}
	if entries[3].GetStream().GetContentType() != dashMIME {
		t.Errorf("%s content type = %q, want the one its MIME type confirmed", entries[3].GetStream().GetUrl(), entries[3].GetStream().GetContentType())
	}
}

// A capture another captured document names is a piece of that program, not a program.
func TestACaptureAnotherDocumentNamesIsDropped(t *testing.T) {
	c := testCollector(t)
	const document = "https://cdn.example/hls/index.m3u8"
	c.addByMIME("https://cdn.example/hls/init.mp4", "req-init", "video/mp4")
	captureWithBody(c, document, "req-doc", "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:6.000,\nseg_00001.m4s\n#EXT-X-ENDLIST\n")
	c.addByMIME("https://cdn.example/hls/seg_00001.m4s", "req-seg", "video/mp4")
	c.addByMIME("https://cdn.example/elsewhere/movie.mp4", "req-movie", "video/mp4")

	if got, want := urls(c.entries()), []string{document, "https://cdn.example/elsewhere/movie.mp4"}; !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

// The body of a redirected request describes, and names relative to, the URL it landed on.
func TestARedirectedDocumentIsReadWhereItLanded(t *testing.T) {
	c := testCollector(t)
	const (
		asked  = "https://embed.example/hls/master.m3u8"
		landed = "https://cdn.example/edge/master.m3u8"
	)
	c.listen(&network.EventRequestWillBeSent{RequestID: "req-1", Request: &network.Request{URL: asked}})
	c.listen(&network.EventRequestWillBeSent{RequestID: "req-1", Request: &network.Request{URL: landed}})
	c.listen(&network.EventResponseReceived{RequestID: "req-1", Response: &network.Response{URL: landed, MimeType: "text/plain"}})

	c.noteDocument("req-1", masterDocument)
	c.addByURL("https://cdn.example/edge/v/1080.m3u8", "req-2")

	entries := c.entries()
	if got := urls(entries); !slices.Equal(got, []string{asked, landed}) {
		t.Fatalf("entries = %v, want both hops and not the variant the landed master names", got)
	}
	if entries[1].Ladder != castorv1.Ladder_LADDER_MULTIVARIANT || entries[0].Ladder != castorv1.Ladder_LADDER_UNSPECIFIED {
		t.Errorf("ladders = %v on %s, %v on %s, want the master on %s", entries[0].Ladder, asked, entries[1].Ladder, landed, landed)
	}
}

// A player printing its stream to the console names it, whatever else it prints.
func TestALinkAPlayerPrintsIsCaptured(t *testing.T) {
	c := testCollector(t)
	c.listen(&runtime.EventConsoleAPICalled{Args: []*runtime.RemoteObject{
		{Value: []byte(`42`)},
		{Value: []byte(`"source: https:\/\/cdn.example\/hls\/index.m3u8?t=\"x\""`)},
		{Value: []byte(`{"src":"https://cdn.example/dash/stream.mpd"}`)},
	}})
	if got, want := urls(c.entries()), []string{"https://cdn.example/hls/index.m3u8?t="}; !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v: only a printed string is read, its escapes undone", got, want)
	}
}

// A store that was never told what it holds answers untyped, and the link's own name says what it is.
func TestAnUntypedResponseIsTypedByItsName(t *testing.T) {
	for _, tc := range []struct {
		raw, mime string
		want      string
	}{
		{"https://bucket.example/movie.mp4", "binary/octet-stream", mp4MIME},
		{"https://bucket.example/movie.mp4", "application/octet-stream", mp4MIME},
		{"https://bucket.example/blob/abc", "application/octet-stream", ""},
		{"https://cdn.example/poster.mp4", "image/jpeg", ""},
	} {
		c := testCollector(t)
		c.addByMIME(tc.raw, "req-1", tc.mime)
		var got string
		if entries := c.entries(); len(entries) > 0 {
			got = entries[0].GetStream().GetContentType()
		}
		if got != tc.want {
			t.Errorf("%s served as %s captured as %q, want %q", tc.raw, tc.mime, got, tc.want)
		}
	}
}

// adDocument is a closed pre-roll: real media too short to be the title.
const adDocument = "#EXTM3U\n#EXTINF:6.000,\nad_1.ts\n#EXTINF:6.000,\nad_2.ts\n#EXT-X-ENDLIST\n"

func featureDocument() string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for i := range 60 {
		fmt.Fprintf(&b, "#EXTINF:6.000,\nseg_%d.ts\n", i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

func timedCollector(t *testing.T, preRoll time.Duration) *collector {
	unreadable := func(network.RequestID) ([]byte, error) { return nil, errors.New("no body") }
	return newCollector(t.Context(), unreadable, time.Second, 50*time.Millisecond, preRoll)
}

// Collection stops at the window once anything but an ad is held, and at the pre-roll's end when nothing is.
func TestCollectionStopsOnceItHoldsMoreThanAds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		atLeast time.Duration
		atMost  time.Duration
	}{
		{"a feature ends it at the window", featureDocument(), 50 * time.Millisecond, 250 * time.Millisecond},
		{"a playlist of unknown length ends it at the window", "#EXTM3U\n#EXTINF:6.000,\nlive.ts\n", 50 * time.Millisecond, 250 * time.Millisecond},
		{"only ads end it at the pre-roll", adDocument, 400 * time.Millisecond, 700 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := timedCollector(t, 400*time.Millisecond)
			captureWithBody(c, "https://cdn.example/index.m3u8", "req-1", tc.body)
			start := time.Now()
			if _, err := c.Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
			if took := time.Since(start); took < tc.atLeast || took > tc.atMost {
				t.Errorf("collection took %v, want between %v and %v", took, tc.atLeast, tc.atMost)
			}
		})
	}
}

func TestLateBrowserHeadersReachTheCaptureWithoutChangingEarlierSnapshots(t *testing.T) {
	c := testCollector(t)
	const raw = "https://cdn.example/master.m3u8"
	c.addByURL(raw, "") // The console can name a stream before its request arrives.
	c.listen(&network.EventRequestWillBeSent{
		RequestID: "req-1",
		Request: &network.Request{URL: raw, Headers: network.Headers{
			"Referer": "https://site.example/watch", "X-Player": "first",
		}},
	})
	before := c.entries()
	c.listen(&network.EventRequestWillBeSentExtraInfo{
		RequestID: "req-1",
		Headers: network.Headers{
			":authority": "cdn.example", "Cookie": "viewer=secret; session=token",
			"Origin": "https://embed.example", "Range": "bytes=0-99",
			"Accept-Encoding": "br", "X-Player": "second", "X-Empty": "",
		},
	})
	after := c.entries()
	if len(before) != 1 || len(after) != 1 {
		t.Fatalf("console/request capture was duplicated: %d before, %d after", len(before), len(after))
	}
	want := map[string]string{
		"Referer": "https://site.example/watch", "Origin": "https://embed.example",
		"Cookie": "viewer=secret; session=token", "X-Player": "second",
	}
	if !maps.Equal(after[0].Stream.Headers, want) {
		t.Errorf("late replay headers: %v, want %v", after[0].Stream.Headers, want)
	}
	if before[0].GetStream().GetHeaders()["X-Player"] != "first" || before[0].GetStream().GetHeaders()["Cookie"] != "" || before[0].GetStream().GetHeaders()["Origin"] != "https://site.example" {
		t.Errorf("earlier capture snapshot changed: %v", before[0].Stream.Headers)
	}
	after[0].Stream.Headers["Cookie"] = "changed"
	if got := c.entries()[0].GetStream().GetHeaders()["Cookie"]; got != "viewer=secret; session=token" {
		t.Errorf("caller changed stored browser headers: %q", got)
	}
}

func TestRedirectHeadersBelongToTheirOwnHop(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("late-extra=%v", late), func(t *testing.T) {
			c := testCollector(t)
			const first, last = "https://site.example/master.m3u8", "https://cdn.example/master.m3u8"
			c.listen(&network.EventRequestWillBeSent{RequestID: "r", Request: &network.Request{URL: first, Headers: network.Headers{"Referer": "https://site.example/watch"}}})
			extra := &network.EventRequestWillBeSentExtraInfo{RequestID: "r", Headers: network.Headers{"Cookie": "site=secret", "Authorization": "Bearer site-token"}}
			if !late {
				c.listen(extra)
			}
			c.listen(&network.EventRequestWillBeSent{RequestID: "r", Request: &network.Request{URL: last, Headers: network.Headers{"Referer": "https://site.example/"}}, RedirectResponse: &network.Response{URL: first}, RedirectHasExtraInfo: true})
			if late {
				c.listen(extra)
			}
			c.listen(&network.EventRequestWillBeSentExtraInfo{RequestID: "r", Headers: network.Headers{"Cookie": "cdn=token"}})
			c.listen(&network.EventResponseReceived{RequestID: "r", Response: &network.Response{URL: last, MimeType: hlsMIME}, HasExtraInfo: true})
			entries := c.entries()
			if len(entries) != 2 {
				t.Fatalf("captures: %v", entries)
			}
			if got := entries[0].GetStream().GetHeaders()["Cookie"]; got != "site=secret" {
				t.Errorf("first hop cookie = %q", got)
			}
			if got := entries[1].GetStream().GetHeaders()["Cookie"]; got != "cdn=token" || entries[1].GetStream().GetHeaders()["Authorization"] != "" {
				t.Errorf("redirect leaked first-hop credentials: %v", entries[1].GetStream().GetHeaders())
			}
		})
	}
}

func TestCanceledCollectionDoesNotReturnCapturedStreams(t *testing.T) {
	c := testCollector(t)
	c.addByURL("https://cdn.example/master.m3u8", "r")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if streams, err := c.Wait(ctx); !errors.Is(err, context.Canceled) || len(streams) != 0 {
		t.Fatalf("canceled collection = %v, %v", streams, err)
	}
}

func TestCollectorDoesNotReadVideoBodiesAsDocuments(t *testing.T) {
	reads := 0
	c := newCollector(t.Context(), func(network.RequestID) ([]byte, error) {
		reads++
		return []byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nother.m3u8\n"), nil
	}, time.Second, time.Second, time.Second)
	c.addByMIME("https://cdn.example/movie.mp4", "video", mp4MIME)
	c.askForDocument("video", 100)
	c.close()
	if reads != 0 || c.entries()[0].Ladder != castorv1.Ladder_LADDER_UNSPECIFIED {
		t.Fatalf("video interpreted as document: reads=%d, captures=%v", reads, c.entries())
	}
}

func TestCollectorRejectsOversizedDecodedDocuments(t *testing.T) {
	c := newCollector(t.Context(), func(network.RequestID) ([]byte, error) {
		return []byte(masterDocument + strings.Repeat("#padding\n", documentSizeLimit/8)), nil
	}, time.Second, time.Second, time.Second)
	c.addByURL("https://cdn.example/master.m3u8", "r")
	c.askForDocument("r", 100) // The wire size may be compressed.
	c.close()
	if c.entries()[0].Ladder != castorv1.Ladder_LADDER_UNSPECIFIED {
		t.Fatal("oversized decoded body was parsed")
	}
}

func TestCollectorRetainsStreamHeadersAfterRequestFlood(t *testing.T) {
	c := testCollector(t)
	for i := range 2 * maxTrackedRequests {
		c.listen(&network.EventRequestWillBeSent{RequestID: network.RequestID(fmt.Sprintf("noise-%d", i)), Request: &network.Request{URL: "https://site.example/telemetry", Headers: network.Headers{"X-Noise": "ignored"}}})
	}
	if len(c.requests) > maxTrackedRequests {
		t.Fatalf("unbounded request bookkeeping: %d records", len(c.requests))
	}
	c.listen(&network.EventRequestWillBeSent{RequestID: "stream", Request: &network.Request{URL: "https://cdn.example/master.m3u8"}})
	headers := network.Headers{"cookie": "viewer=token", "authorization": "Bearer stream-token", "referer": "https://site.example/watch", "If-None-Match": "old-cache", "Connection": "X-Hop", "X-Hop": "connection-private"}
	for i := range 100 {
		headers[fmt.Sprintf("A-Extra-%03d", i)] = "noise"
	}
	c.listen(&network.EventRequestWillBeSentExtraInfo{RequestID: "stream", Headers: headers})
	entries := c.entries()
	if len(entries) != 1 || entries[0].GetStream().GetHeaders()["Cookie"] != "viewer=token" {
		t.Fatalf("noise prevented stream replay: %v", entries)
	}
	if entries[0].GetStream().GetHeaders()["Authorization"] != "Bearer stream-token" || entries[0].GetStream().GetHeaders()["Referer"] != "https://site.example/watch" {
		t.Fatalf("header limits discarded playback credentials: %v", entries[0].Stream.Headers)
	}
	if entries[0].GetStream().GetHeaders()["If-None-Match"] != "" || entries[0].GetStream().GetHeaders()["X-Hop"] != "" {
		t.Fatalf("replay carries cache/connection headers: %v", entries[0].Stream.Headers)
	}
}
