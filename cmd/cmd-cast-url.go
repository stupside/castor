package cmd

import (
	"context"
	"fmt"
	"net/url"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/cast"
	"github.com/stupside/castor/internal/media"
)

func (a *app) castURLCommand() *cli.Command {
	var urlArg string

	return &cli.Command{
		Name:  "url",
		Usage: "Cast a direct video URL",
		Arguments: []cli.Argument{
			&cli.StringArg{
				Name:        "url",
				Destination: &urlArg,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			urlObj, err := url.Parse(urlArg)
			if err != nil {
				return fmt.Errorf("invalid URL %q: %w", urlArg, err)
			}

			if cmd.Bool("dry-run") {
				fmt.Println(urlObj.String())
				return nil
			}

			cfg, err := a.config()
			if err != nil {
				return err
			}

			stream := &media.Stream{URL: urlObj, ContentType: media.DetectFromExtension(urlObj)}
			return cast.Play(ctx, cfg.Playback(), stream)
		},
	}
}
