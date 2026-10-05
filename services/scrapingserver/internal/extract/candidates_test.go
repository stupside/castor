package extract

import (
	"net/http"
	"testing"

	"github.com/chromedp/cdproto/network"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"google.golang.org/protobuf/proto"
)

func TestCandidatesPreserveCaptureEvidence(t *testing.T) {
	const raw = "https://cdn.example/master.m3u8"
	for _, cookieKey := range []string{"Cookie", "cookie", "COOKIE"} {
		t.Run(cookieKey, func(t *testing.T) {
			c := testCollector(t)
			captureWithBody(c, raw, "req", masterDocument)
			c.requests["req"].hops[0].headers = http.Header{
				"Cookie":  {"viewer=secret", "session=token"},
				"Referer": {"https://site.example/watch"},
				"Accept":  {"video/*", "application/x-mpegURL"},
				"X-Empty": {},
			}
			c.requests["req"].hops[0].wireHeaders = toHTTPHeader(network.Headers{cookieKey: "viewer=secret; session=token"})
			streams := c.entries()
			want := &castorv1.StreamCandidate{
				Stream: &castorv1.Stream{
					Url: raw,
					Headers: map[string]string{
						"Cookie":  "viewer=secret; session=token",
						"Referer": "https://site.example/watch",
						"Origin":  "https://site.example",
						"Accept":  "video/*, application/x-mpegURL",
					},
					ContentType: hlsMIME,
				},
				Ladder: castorv1.Ladder_LADDER_MULTIVARIANT,
			}
			if len(streams) != 1 || !proto.Equal(streams[0], want) {
				t.Fatalf("captured evidence lost: %v, want %v", streams, want)
			}
		})
	}
}
