// Package cmd wires the castor command tree. Configuration is loaded lazily
// into a typed struct the subcommand closures share — no metadata maps, no
// runtime type assertions — so commands that don't need a config (scan, info,
// help) never require one.
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/charmbracelet/log"
	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/config"
	"github.com/stupside/castor/internal/version"
)

// app carries state shared by every subcommand.
type app struct {
	configPath string
	debug      bool

	once sync.Once
	cfg  *config.Config
	err  error
}

// config loads the configuration on first use and memoizes the result.
func (a *app) config() (*config.Config, error) {
	a.once.Do(func() {
		a.cfg, a.err = config.Load(a.configPath)
		if a.err == nil {
			slog.Info("config loaded", "path", a.configPath)
		}
	})
	return a.cfg, a.err
}

func Root() *cli.Command {
	a := &app{}

	return &cli.Command{
		Name:    "castor",
		Usage:   "Cast video streams to networked devices",
		Version: version.Version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "config",
				Aliases:     []string{"c"},
				Usage:       "Path to configuration file",
				Value:       "config.yaml",
				Destination: &a.configPath,
			},
			&cli.BoolFlag{
				Name:        "debug",
				Usage:       "Enable debug logging",
				Destination: &a.debug,
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			if a.debug {
				slog.SetDefault(slog.New(
					log.NewWithOptions(os.Stderr, log.Options{
						ReportTimestamp: true,
						TimeFormat:      "15:04:05.000",
						Level:           log.DebugLevel,
					}),
				))
			}
			return ctx, nil
		},
		Commands: []*cli.Command{
			a.castCommand(),
			a.scanCommand(),
			infoCommand(),
		},
	}
}

func infoCommand() *cli.Command {
	return &cli.Command{
		Name:  "info",
		Usage: "Print build information",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			fmt.Printf("version    %s\n", version.Version)
			fmt.Printf("commit     %s\n", version.Commit)
			fmt.Printf("build time %s\n", version.BuildTime)
			return nil
		},
	}
}
