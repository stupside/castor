package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/cmd/internal/process"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/gen/castor/v1/castorv1connect"
	"github.com/stupside/castor/internal/transport"
)

// urfave/cli resolves flags by lineage, so --dry-run works whether typed before or after the subcommand.
func TestADryRunOfALinkNeedsNoConfigAndNoServerWhicheverSideTheFlagIsTyped(t *testing.T) {
	const link = "https://cdn.example/hls/index.m3u8"
	refuse := Local(func(context.Context, *cli.Command, slog.Handler) (transport.Endpoint, bool, func(), error) {
		return transport.Endpoint{}, false, nil, errors.New("a dry run started castor's servers")
	})
	for _, args := range [][]string{
		{"castor", "-c", "absent.yaml", "cast", "--dry-run", "url", link},
		{"castor", "-c", "absent.yaml", "cast", "url", link, "--dry-run"},
	} {
		root := &cli.Command{Name: "castor", Flags: process.Flags, Commands: Commands(refuse)}
		if err := root.Run(t.Context(), args); err != nil {
			t.Errorf("%v: %v; a dry run of a link must not read a config or start a server", args, err)
		}
	}
}

type scanDevices struct{ called bool }

func (s *scanDevices) ListDevices(context.Context, *castorv1.ListDevicesRequest) (*castorv1.ListDevicesResponse, error) {
	s.called = true
	return &castorv1.ListDevicesResponse{}, nil
}

func TestScanUsesTheConfiguredAPIWithoutRequiringACastDevice(t *testing.T) {
	devices := &scanDevices{}
	_, handler := castorv1connect.NewDeviceServiceHandler(devices)
	srv := httptest.NewServer(handler)
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("api:\n  url: "+srv.URL+"\ndevice:\n  name: Living room\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	local := Local(func(context.Context, *cli.Command, slog.Handler) (transport.Endpoint, bool, func(), error) {
		return transport.Endpoint{}, false, nil, errors.New("scan discarded the configured API and started local servers")
	})
	root := &cli.Command{Name: "castor", Flags: process.Flags, Commands: Commands(local)}
	if err := root.Run(t.Context(), []string{"castor", "--config", path, "scan"}); err != nil {
		t.Fatal(err)
	}
	if !devices.called {
		t.Fatal("scan never reached the configured API")
	}
}

func TestMissingCastArgumentsFailBeforeStartingServers(t *testing.T) {
	for _, args := range [][]string{
		{"castor", "cast", "--dry-run", "url"},
		{"castor", "cast", "movie"},
		{"castor", "cast", "episode", "--season", "1", "--episode", "2"},
		{"castor", "cast", "player"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			started := false
			local := Local(func(context.Context, *cli.Command, slog.Handler) (transport.Endpoint, bool, func(), error) {
				started = true
				return transport.Endpoint{}, false, nil, errors.New("server should not start")
			})
			root := &cli.Command{Name: "castor", Flags: process.Flags, Commands: Commands(local)}
			if err := root.Run(t.Context(), args); err == nil || !strings.Contains(err.Error(), "Required argument") {
				t.Errorf("missing argument: got %v", err)
			}
			if started {
				t.Fatal("a missing cast argument started servers")
			}
		})
	}
}
