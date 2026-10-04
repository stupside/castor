package scrapingserver

import (
	"context"
	"log/slog"
	"strings"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/settings"
	"github.com/stupside/castor/internal/transport"
	"github.com/stupside/castor/services/scrapingserver/internal/streaminfo"
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

type extractorResolver struct {
	extractor interface {
		ExtractAll(context.Context, []string) ([]*streaminfo.Stream, error)
	}
}

func (r extractorResolver) Resolve(ctx context.Context, urls []string) ([]*castorv1.StreamCandidate, error) {
	found, err := r.extractor.ExtractAll(ctx, urls)
	if err != nil {
		return nil, err
	}
	out := make([]*castorv1.StreamCandidate, 0, len(found))
	for _, stream := range found {
		headers := map[string]string{}
		for key, values := range stream.Headers {
			if len(values) > 0 {
				separator := ", "
				if strings.EqualFold(key, "Cookie") {
					separator = "; "
				}
				headers[key] = strings.Join(values, separator)
			}
		}
		out = append(out, &castorv1.StreamCandidate{
			Stream:     &castorv1.Stream{Url: stream.URL.String(), Headers: headers, ContentType: stream.ContentType},
			Ladder:     castorv1.Ladder(stream.Ladder),
			SourcePage: stream.SourcePage,
		})
	}
	return out, nil
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
		srv := New(extractorResolver{extractor: newExtractor(cfg.Config)})
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
	srv := New(extractorResolver{extractor: newExtractor(cfg.Config)})
	return transport.Loopback(ctx, srv, cfg.Scraping.Token, srv.Shutdown)
}
