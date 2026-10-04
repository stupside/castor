package mediaserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/gen/castor/media/v1/mediav1connect"
	"github.com/stupside/castor/internal/transport"
)

func TestAServerWithATokenAnswersOnlyThoseCarryingItButHealthChecksAndDevices(t *testing.T) {
	srv := New(Backend{}, &url.URL{Scheme: "http", Host: "127.0.0.1:9"})
	ts := httptest.NewServer(onePort(srv, "secret"))
	t.Cleanup(ts.Close)

	stop := &mediav1.StopRequest{CastId: "none"}
	for _, tc := range []struct {
		name   string
		client *http.Client
		want   connect.Code
	}{
		{"without the token", http.DefaultClient, connect.CodeUnauthenticated},
		{"with another token", transport.Bearer("guess"), connect.CodeUnauthenticated},
		{"with the token, past the door to a cast not found", transport.Bearer("secret"), connect.CodeNotFound},
	} {
		_, err := mediav1connect.NewCastServiceClient(tc.client, ts.URL).Stop(t.Context(), stop)
		if connect.CodeOf(err) != tc.want {
			t.Errorf("a request %s was met with %v, want %v", tc.name, err, tc.want)
		}
	}

	health, err := grpchealth.NewClient(http.DefaultClient, ts.URL).Check(t.Context(), &grpchealth.CheckRequest{})
	if err != nil || health.Status != grpchealth.StatusServing {
		t.Errorf("a health check without the token was met with %v %v, want serving", health, err)
	}

	resp, err := http.Get(ts.URL + "/media/none/1/stream.ts")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || strings.Contains(string(body), "token") {
		t.Errorf("a device fetching the media route was answered %d %q, want not found rather than asked for a token it never has", resp.StatusCode, body)
	}
}
