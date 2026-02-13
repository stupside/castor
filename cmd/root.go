package cmd

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/app"
	"github.com/stupside/castor/internal/scraper"
)

// Root returns the root CLI command.
func Root(ctx context.Context) *cli.Command {
	var configPath string

	return &cli.Command{
		Name:  "castor",
		Usage: "Cast video streams to networked devices",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "config",
				Aliases:     []string{"c"},
				Usage:       "Path to configuration file",
				Value:       "config.yaml",
				Destination: &configPath,
			},
			&cli.BoolFlag{
				Name:  "debug",
				Usage: "Enable debug logging",
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			cfg, err := app.Load(configPath)
			if err != nil {
				return ctx, err
			}
			cmd.Metadata["config"] = cfg
			cmd.Metadata["scrapers"] = scraper.NewRegistryFromConfig(cfg.Scrapers)
			return ctx, nil
		},
		Commands: []*cli.Command{
			castCommand(ctx),
			scanCommand(ctx),
		},
		Metadata: map[string]any{},
	}
}
