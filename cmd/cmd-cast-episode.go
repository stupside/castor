package cmd

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/app"
)

func castEpisodeCommand() *cli.Command {
	var season int
	var episode int
	var itemID string
	var sourceName string

	return &cli.Command{
		Name:  "episode",
		Usage: "Cast a series episode by item ID via a source",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "source",
				Usage:       "Source to use",
				Required:    true,
				Destination: &sourceName,
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
		Arguments: []cli.Argument{
			&cli.StringArg{
				Name:        "itemID",
				Destination: &itemID,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := app.ConfigFrom(cmd)
			if err != nil {
				return err
			}

			src, err := cfg.Source(sourceName)
			if err != nil {
				return err
			}

			return extractAndCast(ctx, cmd, cfg, src.EpisodeURLs(itemID, uint(season), uint(episode)))
		},
	}
}
