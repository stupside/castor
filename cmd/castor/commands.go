package main

import (
	"context"
	"fmt"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/cmd/castor/internal/browse"
	"github.com/stupside/castor/cmd/castor/internal/browse/tmdb"
	"github.com/stupside/castor/cmd/castor/internal/cast"
	"github.com/stupside/castor/cmd/castor/internal/sources"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/settings"
)

const (
	dryRunFlag = "dry-run"
	atFlag     = "at"
)

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
			&cli.StringFlag{Name: atFlag, Usage: "Begin at this position, such as 1h12m or 1:12", Validator: func(s string) error {
				_, err := cast.ParseStart(s)
				return err
			}},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			dry := cmd.Bool(dryRunFlag)
			var cfg Config
			if dry {
				read, err := settings.Load(cmd, interactiveDryRunConfig{})
				if err != nil {
					return err
				}
				cfg.Sources, cfg.TMDB, cfg.Servers = read.Sources, read.TMDB, read.Servers
			} else {
				read, err := settings.Load(cmd, Config{})
				if err != nil {
					return err
				}
				cfg = *read
			}
			if err := cfg.browsable(); err != nil {
				return err
			}
			api, release, err := dial(ctx, cmd, cfg.Servers.API, local)
			if err != nil {
				return err
			}
			defer release()
			to, sel, chosen, err := browse.Run(ctx, api.devices, tmdb.New(cfg.TMDB), cfg.Device, dry, !dry && !cmd.IsSet(atFlag))
			if err != nil || !chosen {
				return err
			}
			urls := cfg.Sources.MovieURLs(sel.TMDBID)
			if sel.Kind == browse.KindEpisode {
				urls = cfg.Sources.EpisodeURLs(sel.TMDBID, sel.Season, sel.Episode)
			}
			if dry {
				return cast.DryRun(ctx, api.casts, pages(urls))
			}
			start, err := startAt(cmd, sel.Start)
			if err != nil {
				return err
			}
			fmt.Printf("Casting: %s\n", sel.Title)
			return cast.Run(ctx, api.casts, to, pages(urls), &castorv1.MediaInfo{Title: sel.Title, Synopsis: sel.Synopsis, ImageUrl: sel.ImageURL, ReleaseDate: sel.ReleaseDate}, start)
		},
		Commands: []*cli.Command{
			urlCommand(local),
			pagesCommand(local, "movie", "Cast a movie by item ID", "itemID", nil, func(sites sources.Sites, itemID string, _ *cli.Command) []string {
				return sites.MovieURLs(itemID)
			}),
			pagesCommand(local, "episode", "Cast a series episode by item ID", "itemID", []cli.Flag{
				&cli.UintFlag{Name: "season", Usage: "Season number", Required: true},
				&cli.UintFlag{Name: "episode", Usage: "Episode number", Required: true},
			}, func(sites sources.Sites, itemID string, cmd *cli.Command) []string {
				return sites.EpisodeURLs(itemID, cmd.Uint("season"), cmd.Uint("episode"))
			}),
			playerCommand(local),
		},
	}
}

func playerCommand(local Local) *cli.Command {
	var page string
	return &cli.Command{
		Name:      "player",
		Usage:     "Cast a video from a direct player URL",
		Arguments: []cli.Argument{&cli.StringArg{Name: "url", Destination: &page, Required: true}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool(dryRunFlag) {
				cfg, err := settings.Load(cmd, apiConfig{})
				if err != nil {
					return err
				}
				return castSource(ctx, cmd, local, cfg.Servers.API, cast.Device{}, pages([]string{page}))
			}
			cfg, err := settings.Load(cmd, targetConfig{})
			if err != nil {
				return err
			}
			return castSource(ctx, cmd, local, cfg.Servers.API, cfg.Device, pages([]string{page}))
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
			cfg, err := settings.Load(cmd, targetConfig{})
			if err != nil {
				return err
			}
			return castSource(ctx, cmd, local, cfg.Servers.API, cfg.Device,
				&castorv1.Source{Source: &castorv1.Source_Stream{Stream: &castorv1.Stream{Url: link}}})
		},
	}
}

// pagesCommand casts the pages pages finds for its one argument.
func pagesCommand(local Local, name, usage, arg string, flags []cli.Flag, pagesOf func(sources.Sites, string, *cli.Command) []string) *cli.Command {
	var value string
	return &cli.Command{
		Name:      name,
		Usage:     usage,
		Flags:     flags,
		Arguments: []cli.Argument{&cli.StringArg{Name: arg, Destination: &value, Required: true}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool(dryRunFlag) {
				cfg, err := settings.Load(cmd, pageDryRunConfig{})
				if err != nil {
					return err
				}
				return castSource(ctx, cmd, local, cfg.Servers.API, cast.Device{},
					pages(pagesOf(cfg.Sources, value, cmd)))
			}
			cfg, err := settings.Load(cmd, pageCastConfig{})
			if err != nil {
				return err
			}
			return castSource(ctx, cmd, local, cfg.Servers.API, cfg.Device, pages(pagesOf(cfg.Sources, value, cmd)))
		},
	}
}

// pages is a source whose URLs the API server asks scrapingserver to resolve.
func pages(urls []string) *castorv1.Source {
	return &castorv1.Source{Source: &castorv1.Source_Pages_{Pages: &castorv1.Source_Pages{Urls: urls}}}
}

// castSource casts source to device, or asks the API to resolve it for a dry run.
func castSource(ctx context.Context, cmd *cli.Command, local Local, endpoint settings.Endpoint, device cast.Device, source *castorv1.Source) error {
	api, release, err := dial(ctx, cmd, endpoint, local)
	if err != nil {
		return err
	}
	defer release()
	if cmd.Bool(dryRunFlag) {
		return cast.DryRun(ctx, api.casts, source)
	}
	to, err := cast.Target(ctx, api.devices, device)
	if err != nil {
		return err
	}
	start, err := startAt(cmd, 0)
	if err != nil {
		return err
	}
	return cast.Run(ctx, api.casts, to, source, nil, start)
}

func startAt(cmd *cli.Command, chosen time.Duration) (time.Duration, error) {
	if cmd.IsSet(atFlag) {
		return cast.ParseStart(cmd.String(atFlag))
	}
	return chosen, nil
}

func scanCommand(local Local) *cli.Command {
	return &cli.Command{
		Name:  "scan",
		Usage: "List all devices on the local network",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// Scanning needs only the API endpoint; a device is what it discovers.
			cfg, err := settings.Load(cmd, struct {
				Servers struct {
					API settings.Endpoint `yaml:"api"`
				} `yaml:"servers"`
			}{})
			if err != nil {
				return err
			}
			api, release, err := dial(ctx, cmd, cfg.Servers.API, local)
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
				fmt.Printf("%s\t%s\t%s\t%s\n", d.GetName(), cast.DeviceTypeName(d.GetType()), d.GetAddress(), d.GetId())
			}
			return nil
		},
	}
}
