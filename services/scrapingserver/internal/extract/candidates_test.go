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

func TestDeduplicateKeepsDistinctReplayHeaders(t *testing.T) {
	const url = "https://cdn.example/master.m3u8"
	first := &castorv1.StreamCandidate{Stream: &castorv1.Stream{Url: url, Headers: map[string]string{"Cookie": "viewer=first"}}, SourcePage: "https://site.example/first"}
	second := &castorv1.StreamCandidate{Stream: &castorv1.Stream{Url: url, Headers: map[string]string{"Cookie": "viewer=second"}}, SourcePage: "https://site.example/second"}
	better := &castorv1.StreamCandidate{Stream: &castorv1.Stream{Url: url, Headers: map[string]string{"Cookie": "viewer=first"}, ContentType: hlsMIME}, Ladder: castorv1.Ladder_LADDER_MULTIVARIANT}

	got := deduplicate([]*castorv1.StreamCandidate{first, second, better})
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want both access paths", len(got))
	}
	if got[0].GetStream().GetHeaders()["Cookie"] != "viewer=first" || got[1].GetStream().GetHeaders()["Cookie"] != "viewer=second" {
		t.Fatalf("lost access paths: %v", got)
	}
	if got[0].GetLadder() != castorv1.Ladder_LADDER_MULTIVARIANT || got[0].GetStream().GetContentType() != hlsMIME {
		t.Fatalf("lost later document evidence: %v", got[0])
	}
	if first.GetLadder() != castorv1.Ladder_LADDER_UNSPECIFIED || first.GetStream().GetContentType() != "" {
		t.Fatal("deduplication changed its input")
	}
}

func TestDeduplicatePrefersMultivariantEvidence(t *testing.T) {
	const url = "https://cdn.example/playlist.m3u8"
	first := &castorv1.StreamCandidate{Stream: &castorv1.Stream{Url: url}, Ladder: castorv1.Ladder_LADDER_SOLE}
	later := &castorv1.StreamCandidate{Stream: &castorv1.Stream{Url: url}, Ladder: castorv1.Ladder_LADDER_MULTIVARIANT}

	got := deduplicate([]*castorv1.StreamCandidate{first, later})
	if len(got) != 1 || got[0].GetLadder() != castorv1.Ladder_LADDER_MULTIVARIANT {
		t.Fatalf("lost multivariant evidence: %v", got)
	}
	if first.GetLadder() != castorv1.Ladder_LADDER_SOLE {
		t.Fatal("deduplication changed its input")
	}
}

func TestAStreamTheContractRefusesCostsOnlyItself(t *testing.T) {
	good := &castorv1.StreamCandidate{Stream: &castorv1.Stream{Url: "https://cdn.example/master.m3u8"}, SourcePage: "https://site.example/watch"}
	raw := &castorv1.StreamCandidate{Stream: &castorv1.Stream{Url: "https://cdn.example/master.m3u8?token=a|b"}, SourcePage: "https://site.example/watch"}
	if got := accepted(t.Context(), []*castorv1.StreamCandidate{raw, good}); len(got) != 1 || got[0] != good {
		t.Errorf("accepted %v, want only the candidate the contract takes", got)
	}
}
