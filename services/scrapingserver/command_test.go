package scrapingserver

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"google.golang.org/protobuf/proto"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/scrapingserver/internal/streaminfo"
)

type captured []*streaminfo.Stream

func (c captured) ExtractAll(context.Context, []string) ([]*streaminfo.Stream, error) { return c, nil }

func TestCandidateWirePreservesCaptureEvidence(t *testing.T) {
	u, _ := url.Parse("https://cdn.example/master.m3u8")
	for _, cookieKey := range []string{"Cookie", "cookie", "COOKIE"} {
		t.Run(cookieKey, func(t *testing.T) {
			r := extractorResolver{extractor: captured{{
				URL: u,
				Headers: http.Header{
					cookieKey: {"viewer=secret", "session=token"},
					"Referer": {"https://site.example/watch"},
					"Accept":  {"video/*", "application/x-mpegURL"},
					"X-Empty": {},
				},
				ContentType: streaminfo.HLS, Ladder: streaminfo.LadderMultivariant, SourcePage: "https://site.example/watch",
			}}}
			streams, err := r.Resolve(t.Context(), []string{"https://site.example/watch"})
			if err != nil {
				t.Fatal(err)
			}
			want := &castorv1.StreamCandidate{
				Stream: &castorv1.Stream{
					Url: u.String(),
					Headers: map[string]string{
						cookieKey: "viewer=secret; session=token",
						"Referer": "https://site.example/watch",
						"Accept":  "video/*, application/x-mpegURL",
					},
					ContentType: streaminfo.HLS,
				},
				Ladder: castorv1.Ladder_LADDER_MULTIVARIANT, SourcePage: "https://site.example/watch",
			}
			if len(streams) != 1 || !proto.Equal(streams[0], want) {
				t.Fatalf("captured evidence lost: %v, want %v", streams, want)
			}
		})
	}
}
