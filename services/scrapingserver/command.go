package scrapingserver

import (
	"context"
	"log/slog"

	"github.com/stupside/castor/internal/settings"
	"github.com/stupside/castor/internal/transport"
	"github.com/urfave/cli/v3"
)

type ServerConfig struct {
	Listen string `yaml:"listen" validate:"required,hostname_port|startswith=:"`
	Token  string `yaml:"token"`
}

type ServiceConfig struct {
	Config   `yaml:",inline"`
	Scraping ServerConfig `yaml:"scraping" validate:"required"`
}

func defaults() ServiceConfig {
	return ServiceConfig{Config: Defaults(), Scraping: ServerConfig{Listen: ":8412"}}
}

func Command() *cli.Command {
	return &cli.Command{Name: "scraping-server", Usage: "Resolve web pages into playable stream candidates", Action: func(ctx context.Context, cmd *cli.Command) error {
		cfg, err := settings.Load(cmd, defaults())
		if err != nil {
			return err
		}
		listener, err := transport.Listen(ctx, cfg.Scraping.Listen, cfg.Scraping.Token, "scraping.token")
		if err != nil {
			return err
		}
		srv := New(newExtractor(cfg.Config))
		slog.InfoContext(ctx, "scraping serving", "address", listener.Addr().String())
		return transport.Serve(ctx, listener, transport.Authorized(srv, cfg.Scraping.Token), srv.Shutdown)
	}}
}

// Embedded starts the resolver on loopback when the CLI has no remote scrapingserver.
func Embedded(ctx context.Context, cmd *cli.Command) (transport.Endpoint, func(), error) {
	cfg, err := settings.Load(cmd, defaults())
	if err != nil {
		return transport.Endpoint{}, nil, err
	}
	srv := New(newExtractor(cfg.Config))
	return transport.Loopback(ctx, srv, cfg.Scraping.Token, srv.Shutdown)
}
