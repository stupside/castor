package cmd

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/app"
)

func castPlayerCommand() *cli.Command {
	var pageURL string

	return &cli.Command{
		Name:  "player",
		Usage: "Cast a video from a direct player URL",
		Arguments: []cli.Argument{
			&cli.StringArg{
				Name:        "url",
				Destination: &pageURL,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := app.ConfigFrom(cmd)
			if err != nil {
				return err
			}

			return extractAndCast(ctx, cmd, cfg, []string{pageURL})
		},
	}
}
