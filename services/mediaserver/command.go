package mediaserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/settings"
	"github.com/stupside/castor/internal/transport"
	"github.com/stupside/castor/services/mediaserver/internal/castlog"
	"github.com/stupside/castor/services/mediaserver/internal/mediaroute"
)

// Command is `castor media-server`: the media server alone, serving the API servers that reach it and their devices until interrupted.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "media-server",
		Usage: "Run the media server castor's API servers cast through, and serve their devices, until interrupted",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := settings.Load(cmd, defaults())
			if err != nil {
				return err
			}
			l, err := transport.Listen(ctx, cfg.Server.Listen, cfg.Server.Token, "server.token")
			if err != nil {
				return err
			}
			defer l.Close()
			reach, err := cfg.advertised(ctx, l)
			if err != nil {
				return fmt.Errorf("resolving where devices reach this server (set server.advertise): %w", err)
			}
			// A detached media server keeps every line on its own output too; watchers get their casts' lines live.
			h := slog.Default().Handler()
			slog.SetDefault(slog.New(castlog.Router(h, h)))
			slog.InfoContext(ctx, "serving", "address", l.Addr().String(), "devices_reach", reach.String())
			srv := New(cfg.backend(), reach)
			return transport.Serve(ctx, l, onePort(srv, cfg.Server.Token), srv.Shutdown)
		},
	}
}

// onePort serves the media server's API behind token and its media route beside it, which devices fetch without the token they never have.
func onePort(srv *Server, token string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", transport.Authorized(srv.API, token))
	mux.Handle(mediaroute.Pattern, srv.Media)
	return mux
}

// Embedded runs a media server in this process until stop, its own lines going to lines: its API on loopback, its media route where devices on this network reach it.
func Embedded(ctx context.Context, cmd *cli.Command, lines slog.Handler) (transport.Endpoint, func(), error) {
	cfg, err := settings.Load(cmd, defaults())
	if err != nil {
		return transport.Endpoint{}, nil, err
	}
	slog.SetDefault(slog.New(castlog.Router(slog.Default().Handler(), lines)))
	lan, err := cfg.deviceListener(ctx)
	if err != nil {
		return transport.Endpoint{}, nil, fmt.Errorf("opening where devices reach this machine: %w", err)
	}
	reach := &url.URL{Scheme: "http", Host: lan.Addr().String()}
	srv := New(cfg.backend(), reach)
	api, stopAPI, err := transport.Loopback(ctx, srv.API, cfg.Server.Token, srv.Shutdown)
	if err != nil {
		_ = lan.Close()
		return transport.Endpoint{}, nil, err
	}
	slog.InfoContext(ctx, "embedded media server ready", "api", api.URL, "devices_reach", reach.String())
	stopMedia := transport.Background(ctx, lan, srv.Media, srv.Shutdown)
	stop := func() {
		stopAPI()
		stopMedia()
	}
	return api, stop, nil
}
