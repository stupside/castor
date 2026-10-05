package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/cmd/castor/internal/browse"
	"github.com/stupside/castor/cmd/castor/internal/browse/tmdb"
	"github.com/stupside/castor/cmd/castor/internal/cast"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/settings"
)

const dryRunFlag = "dry-run"

// Commands are the command line's commands, reaching the API config names or the one local runs.
func Commands(local Local) []*cli.Command {
	return []*cli.Command{castCommand(local), scanCommand(local)}
}

func castCommand(local Local) *cli.Command {
	return &cli.Command{
		Name:  "cast",
		Usage: "Browse and cast to a device",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: dryRunFlag, Aliases: []string{"d"}, Usage: "Print found streaming URLs instead of casting"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := settings.Load(cmd, Config{})
			if err != nil {
				return err
			}
			if err := cfg.browsable(); err != nil {
				return err
			}
			api, lines, release, err := dial(ctx, cmd, cfg, local)
			if err != nil {
				return err
			}
			defer release()
			to, sel, chosen, err := browse.Run(ctx, api.devices, tmdb.New(cfg.TMDB), cfg.Device)
			if err != nil || !chosen {
				return err
			}
			urls := cfg.Sources.MovieURLs(sel.TMDBID)
			if sel.Kind == browse.KindEpisode {
				urls = cfg.Sources.EpisodeURLs(sel.TMDBID, sel.Season, sel.Episode)
			}
			fmt.Printf("Casting: %s\n", sel.Title)
			if cmd.Bool(dryRunFlag) {
				return cast.DryRun(ctx, api.casts, pages(urls))
			}
			return cast.Run(ctx, api.casts, to, pages(urls), lines)
		},
		Commands: []*cli.Command{
			urlCommand(local),
			pagesCommand(local, "movie", "Cast a movie by item ID", "itemID", nil, func(cfg *Config, itemID string, _ *cli.Command) []string {
				return cfg.Sources.MovieURLs(itemID)
			}),
			pagesCommand(local, "episode", "Cast a series episode by item ID", "itemID", []cli.Flag{
				&cli.UintFlag{Name: "season", Usage: "Season number", Required: true},
				&cli.UintFlag{Name: "episode", Usage: "Episode number", Required: true},
			}, func(cfg *Config, itemID string, cmd *cli.Command) []string {
				return cfg.Sources.EpisodeURLs(itemID, cmd.Uint("season"), cmd.Uint("episode"))
			}),
			pagesCommand(local, "player", "Cast a video from a direct player URL", "url", nil, func(_ *Config, page string, _ *cli.Command) []string {
				return []string{page}
			}),
		},
	}
}

func urlCommand(local Local) *cli.Command {
	var link string
	return &cli.Command{
		Name:      "url",
		Usage:     "Cast a direct video URL",
		Arguments: []cli.Argument{&cli.StringArg{Name: "url", Destination: &link, Required: true}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// A stream as is ranks to itself, so a dry run needs neither config nor servers.
			if cmd.Bool(dryRunFlag) {
				fmt.Println(link)
				return nil
			}
			return castSource(ctx, cmd, local, func(*Config) *castorv1.Source {
				return &castorv1.Source{Source: &castorv1.Source_Stream{Stream: &castorv1.Stream{Url: link}}}
			})
		},
	}
}

// pagesCommand casts the pages pages finds for its one argument.
func pagesCommand(local Local, name, usage, arg string, flags []cli.Flag, pagesOf func(cfg *Config, value string, cmd *cli.Command) []string) *cli.Command {
	var value string
	return &cli.Command{
		Name:      name,
		Usage:     usage,
		Flags:     flags,
		Arguments: []cli.Argument{&cli.StringArg{Name: arg, Destination: &value, Required: true}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return castSource(ctx, cmd, local, func(cfg *Config) *castorv1.Source { return pages(pagesOf(cfg, value, cmd)) })
		},
	}
}

// pages is a source whose URLs the API server asks scrapingserver to resolve.
func pages(urls []string) *castorv1.Source {
	return &castorv1.Source{Source: &castorv1.Source_Pages_{Pages: &castorv1.Source_Pages{Urls: urls}}}
}

// castSource casts the source source makes of cfg to the device cfg names, or prints what it would play.
func castSource(ctx context.Context, cmd *cli.Command, local Local, source func(*Config) *castorv1.Source) error {
	cfg, err := settings.Load(cmd, Config{})
	if err != nil {
		return err
	}
	api, lines, release, err := dial(ctx, cmd, cfg, local)
	if err != nil {
		return err
	}
	defer release()
	if cmd.Bool(dryRunFlag) {
		return cast.DryRun(ctx, api.casts, source(cfg))
	}
	to, err := cast.Target(ctx, api.devices, cfg.Device)
	if err != nil {
		return err
	}
	return cast.Run(ctx, api.casts, to, source(cfg), lines)
}

func scanCommand(local Local) *cli.Command {
	return &cli.Command{
		Name:  "scan",
		Usage: "List all devices on the local network",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// Scanning needs only the API endpoint; a device is what it discovers.
			cfg, err := settings.Load(cmd, struct {
				API apiConfig `yaml:"api"`
			}{})
			if err != nil {
				return err
			}
			api, _, release, err := dial(ctx, cmd, &Config{API: cfg.API}, local)
			if err != nil {
				return err
			}
			defer release()
			listed, err := api.devices.ListDevices(ctx, &castorv1.ListDevicesRequest{})
			if err != nil {
				return err
			}
			if len(listed.GetDevices()) == 0 {
				fmt.Println("no devices found")
			}
			for _, d := range listed.GetDevices() {
				fmt.Printf("%s\t%s\t%s\n", d.GetName(), cast.DeviceTypeName(d.GetType()), d.GetAddress())
			}
			return nil
		},
	}
}
