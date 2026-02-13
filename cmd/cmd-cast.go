package cmd

import (
	"context"

	"github.com/urfave/cli/v3"
)

// castCommand returns the "cast" CLI subcommand.
func castCommand(_ context.Context) *cli.Command {
	return &cli.Command{
		Name:  "cast",
		Usage: "Cast a video to the default device",
		Commands: []*cli.Command{
			castURLCommand(),
			castMovieCommand(),
			castEpisodeCommand(),
		},
	}
}
