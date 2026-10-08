package apiserver

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/settings"
	"github.com/stupside/castor/internal/transport"
)

// Media runs a media server in this process, for an API server that names none, its own lines going to lines.
type Media func(ctx context.Context, cmd *cli.Command, lines slog.Handler) (transport.Endpoint, func(), error)
type Scraping func(context.Context, *cli.Command) (transport.Endpoint, func(), error)

// Command is `castor api-server`: the API server alone, casting through the media server server.url names, until interrupted.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "api-server",
		Usage: "Serve castor's API on the devices on this network, casting through the media server server.url names, until interrupted",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := settings.Load(cmd, defaults())
			if err != nil {
				return err
			}
			if cfg.Server.URL == "" {
				return errors.New("server.url is required: the API server casts through a media server (`castor media-server`)")
			}
			scraping := transport.Endpoint{URL: cmp.Or(cfg.Scraping.URL, "http://localhost:8412"), Token: cfg.Scraping.Token}
			srv, err := New(cfg.backend(transport.Endpoint{URL: cfg.Server.URL, Token: cfg.Server.Token}, scraping), cfg.Cast)
			if err != nil {
				return fmt.Errorf("validating config: %w", err)
			}
			l, err := transport.Listen(ctx, cfg.API.Listen, cfg.API.Token, "api.token")
			if err != nil {
				return err
			}
			slog.InfoContext(ctx, "api serving", "address", l.Addr().String())
			return transport.Serve(ctx, l, transport.Authorized(srv, cfg.API.Token), srv.Shutdown)
		},
	}
}

// Embedded runs an API server in this process until stop, on loopback, its media server's own lines going to lines when it runs one too, which written reports.
func Embedded(media Media, scraping Scraping) func(ctx context.Context, cmd *cli.Command, lines slog.Handler) (api transport.Endpoint, written bool, stop func(), err error) {
	return func(ctx context.Context, cmd *cli.Command, lines slog.Handler) (transport.Endpoint, bool, func(), error) {
		cfg, err := settings.Load(cmd, defaults())
		if err != nil {
			return transport.Endpoint{}, false, nil, err
		}
		srv, stopMedia, err := cfg.server(ctx, cmd, media, scraping, lines)
		if err != nil {
			return transport.Endpoint{}, false, nil, err
		}
		api, stopAPI, err := transport.Loopback(ctx, srv, cfg.API.Token, srv.Shutdown)
		if err != nil {
			stopMedia()
			return transport.Endpoint{}, false, nil, err
		}
		stop := func() {
			stopAPI()
			stopMedia()
		}
		return api, cfg.Server.URL == "", stop, nil
	}
}

// server is the API server cfg describes, casting through the media server it names, or one media runs here; stop ends the one run here.
func (c *Config) server(ctx context.Context, cmd *cli.Command, media Media, resolver Scraping, lines slog.Handler) (srv *Server, stop func(), err error) {
	at, stop := transport.Endpoint{URL: c.Server.URL, Token: c.Server.Token}, func() {}
	if at.URL == "" {
		if at, stop, err = media(ctx, cmd, lines); err != nil {
			return nil, nil, err
		}
	}
	scraping := transport.Endpoint{URL: c.Scraping.URL, Token: c.Scraping.Token}
	stopScraping := func() {}
	if scraping.URL == "" {
		if scraping, stopScraping, err = resolver(ctx, cmd); err != nil {
			stop()
			return nil, nil, err
		}
	}
	stopMedia := stop
	stop = func() { stopScraping(); stopMedia() }
	if srv, err = New(c.backend(at, scraping), c.Cast); err != nil {
		stop()
		return nil, nil, fmt.Errorf("validating config: %w", err)
	}
	return srv, stop, nil
}
