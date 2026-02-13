package cmd

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/app"
)

func castMovieCommand() *cli.Command {
	var itemID string
	var sourceName string

	return &cli.Command{
		Name:  "movie",
		Usage: "Cast a movie by item ID via a source",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "source",
				Usage:       "Source to use",
				Required:    true,
				Destination: &sourceName,
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

			return extractAndCast(ctx, cmd, cfg, src.MovieURLs(itemID))
		},
	}
}
