package mediaserver

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"github.com/urfave/cli/v3"

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

func TestFailedAddressDiscoveryReleasesTheListenPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	t.Setenv("CASTOR_SERVER__LISTEN", addr)
	t.Setenv("CASTOR_SERVER__ADVERTISE", "")
	t.Setenv("CASTOR_NETWORK__INTERFACE", "castor-interface-that-does-not-exist")
	cmd := Command()
	cmd.Flags = []cli.Flag{&cli.StringFlag{Name: "config", Value: filepath.Join(t.TempDir(), "absent.yaml")}}
	if err := cmd.Run(t.Context(), []string{"media-server"}); err == nil || !strings.Contains(err.Error(), "resolving where devices reach this server") {
		t.Fatalf("starting on an absent interface: %v", err)
	}
	reopened, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed startup still holds the media server port: %v", err)
	}
	_ = reopened.Close()
}
