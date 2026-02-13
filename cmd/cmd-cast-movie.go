package cmd

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/app"
	"github.com/stupside/castor/internal/cast"
	"github.com/stupside/castor/internal/scraper"
)

func castMovieCommand() *cli.Command {
	var scraperName string

	return &cli.Command{
		Name:      "movie",
		Usage:     "Cast a movie by item ID via a scraper",
		ArgsUsage: "<itemID>",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "scraper",
				Usage:       "Scraper to use",
				Required:    true,
				Destination: &scraperName,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := app.ConfigFrom(cmd)
			if err != nil {
				return err
			}

			itemID := cmd.Args().First()
			if itemID == "" {
				return cli.Exit("provide an item ID argument", 1)
			}

			registry, ok := cmd.Root().Metadata["scrapers"].(*scraper.Registry)
			if !ok {
				return fmt.Errorf("scrapers not initialized")
			}
			return cast.NewService(cfg, registry).CastMovie(ctx, scraperName, itemID)
		},
	}
}
