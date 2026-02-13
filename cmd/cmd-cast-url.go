package cmd

import (
	"context"
	"fmt"
	"net/url"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/app"
	"github.com/stupside/castor/internal/cast"
	"github.com/stupside/castor/internal/scraper"
)

func castURLCommand() *cli.Command {
	return &cli.Command{
		Name:      "url",
		Usage:     "Cast a direct video URL",
		ArgsUsage: "<url>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := app.ConfigFrom(cmd)
			if err != nil {
				return err
			}

			raw := cmd.Args().First()
			if raw == "" {
				return cli.Exit("provide a URL argument", 1)
			}

			itemURL, err := url.Parse(raw)
			if err != nil {
				return fmt.Errorf("invalid URL %q: %w", raw, err)
			}

			registry, ok := cmd.Root().Metadata["scrapers"].(*scraper.Registry)
			if !ok {
				return fmt.Errorf("scrapers not initialized")
			}
			return cast.NewService(cfg, registry).CastURL(ctx, itemURL)
		},
	}
}
