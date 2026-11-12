package apiserver

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/urfave/cli/v3"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/settings"
	"github.com/stupside/castor/internal/transport"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

// Media runs a media server in this process, for an API server that names none.
type Media func(ctx context.Context, cmd *cli.Command) (transport.Endpoint, func(), error)
type Scraping func(context.Context, *cli.Command) (transport.Endpoint, func(), error)

// DeviceDiscovery supplies devices found outside Castor's Go network discovery.
type DeviceDiscovery func(context.Context) ([]*castorv1.Device, error)

// Command is `castor api-server`: the API server alone, casting through the media server servers.media.url names, until interrupted.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "api-server",
		Usage: "Serve castor's API on the devices on this network, casting through the media server servers.media.url names, until interrupted",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := settings.Load(cmd, defaults())
			if err != nil {
				return err
			}
			if cfg.Servers.Media.URL == "" {
				return errors.New("servers.media.url is required: the API server casts through a media server (`castor media-server`)")
			}
			scraping := cfg.Servers.Scraping
			scraping.URL = cmp.Or(scraping.URL, "http://localhost:8412")
			srv, err := New(cfg.backend(cfg.Servers.Media, scraping), cfg.Cast)
			if err != nil {
				return fmt.Errorf("validating config: %w", err)
			}
			l, err := transport.Listen(ctx, cfg.Servers.API.Listen, cfg.Servers.API.Token, "servers.api.token")
			if err != nil {
				return err
			}
			slog.InfoContext(ctx, "api serving", "address", l.Addr().String())
			return transport.Serve(ctx, l, transport.Authorized(srv, cfg.Servers.API.Token), srv.Shutdown)
		},
	}
}

// Embedded runs an API server in this process until stop, on loopback.
func Embedded(media Media, scraping Scraping) func(ctx context.Context, cmd *cli.Command) (api transport.Endpoint, stop func(), err error) {
	return EmbeddedWithDiscovery(media, scraping, nil)
}

// EmbeddedWithDiscovery accepts an external device source while Castor retains device control.
func EmbeddedWithDiscovery(media Media, scraping Scraping, discovery DeviceDiscovery) func(ctx context.Context, cmd *cli.Command) (api transport.Endpoint, stop func(), err error) {
	return func(ctx context.Context, cmd *cli.Command) (transport.Endpoint, func(), error) {
		cfg, err := settings.Load(cmd, defaults())
		if err != nil {
			return transport.Endpoint{}, nil, err
		}
		srv, stopMedia, err := cfg.server(ctx, cmd, media, scraping, discovery)
		if err != nil {
			return transport.Endpoint{}, nil, err
		}
		api, stopAPI, err := transport.Loopback(ctx, srv, cfg.Servers.API.Token, srv.Shutdown)
		if err != nil {
			stopMedia()
			return transport.Endpoint{}, nil, err
		}
		stop := func() {
			stopAPI()
			stopMedia()
		}
		return api, stop, nil
	}
}

// server is the API server cfg describes, casting through the media server it names, or one media runs here; stop ends the one run here.
func (c *Config) server(ctx context.Context, cmd *cli.Command, media Media, resolver Scraping, discovery DeviceDiscovery) (srv *Server, stop func(), err error) {
	at, stop := c.Servers.Media, func() {}
	if at.URL == "" {
		if at, stop, err = media(ctx, cmd); err != nil {
			return nil, nil, err
		}
	}
	scraping := c.Servers.Scraping
	stopScraping := func() {}
	if scraping.URL == "" {
		if scraping, stopScraping, err = resolver(ctx, cmd); err != nil {
			stop()
			return nil, nil, err
		}
	}
	stopMedia := stop
	stop = func() { stopScraping(); stopMedia() }
	backend := c.backend(at, scraping)
	if discovery != nil {
		backend.Discover = device.ExternalDiscovery(discovery)
	}
	if srv, err = New(backend, c.Cast); err != nil {
		stop()
		return nil, nil, fmt.Errorf("validating config: %w", err)
	}
	return srv, stop, nil
}
