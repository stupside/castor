package cmd

import (
	"context"
	"fmt"

	"github.com/stupside/castor/internal/app"
	"github.com/stupside/castor/internal/cast"
	"github.com/stupside/castor/internal/extractor"
	"github.com/stupside/castor/internal/media"
	"github.com/stupside/castor/internal/resolve"
	"github.com/urfave/cli/v3"
)

// castCommand returns the "cast" CLI subcommand.
func castCommand() *cli.Command {
	return &cli.Command{
		Name:  "cast",
		Usage: "Cast a video to the default device",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "dry-run",
				Aliases: []string{"d"},
				Usage:   "Print found streaming URLs instead of casting",
			},
		},
		Commands: []*cli.Command{
			castURLCommand(),
			castMovieCommand(),
			castEpisodeCommand(),
			castPlayerCommand(),
		},
	}
}

// extractAndCast creates an extractor, extracts streams from the given URLs,
// and either lists them (--dry-run) or casts the best one.
func extractAndCast(ctx context.Context, cmd *cli.Command, cfg *app.Config, urls []string) error {
	ext, err := extractor.NewExtractor(cfg.Browser, cfg.Capture, cfg.Actions)
	if err != nil {
		return fmt.Errorf("creating extractor: %w", err)
	}

	streams, err := extractor.ExtractAll(ctx, ext, urls)
	if err != nil {
		return fmt.Errorf("extracting streams: %w", err)
	}

	return handleStreams(ctx, cmd, cfg, streams)
}

// handleStreams handles the --dry-run / cast logic shared by player, movie, and episode commands.
func handleStreams(ctx context.Context, cmd *cli.Command, cfg *app.Config, streams []*media.Stream) error {
	if cmd.Bool("dry-run") {
		for _, d := range resolve.ListStreams(ctx, cfg.Resolver, streams) {
			fmt.Printf("%d\t%s\n", d.BitRate, d.URL)
		}
		return nil
	}

	best, err := resolve.RankStreams(ctx, cfg.Resolver, streams)
	if err != nil {
		return fmt.Errorf("ranking streams: %w", err)
	}

	return cast.CastStream(ctx, cfg, best)
}
