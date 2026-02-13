package cmd

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/app"
	"github.com/stupside/castor/internal/cast"
	"github.com/stupside/castor/internal/scraper"
)

func castEpisodeCommand() *cli.Command {
	var scraperName string
	var season int64
	var episode int64

	return &cli.Command{
		Name:      "episode",
		Usage:     "Cast a series episode by item ID via a scraper",
		ArgsUsage: "<itemID>",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "scraper",
				Usage:       "Scraper to use",
				Required:    true,
				Destination: &scraperName,
			},
			&cli.IntFlag{
				Name:        "season",
				Usage:       "Season number",
				Required:    true,
				Destination: &season,
			},
			&cli.IntFlag{
				Name:        "episode",
				Usage:       "Episode number",
				Required:    true,
				Destination: &episode,
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
			return cast.NewService(cfg, registry).CastEpisode(ctx, scraperName, itemID, uint(season), uint(episode))
		},
	}
}
