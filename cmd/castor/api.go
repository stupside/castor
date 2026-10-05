package main

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	"connectrpc.com/validate"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/cmd/castor/internal/cast"
	"github.com/stupside/castor/cmd/internal/process"
	"github.com/stupside/castor/gen/castor/v1/castorv1connect"
	"github.com/stupside/castor/internal/transport"
)

// Local runs castor's API in this process, its servers' own lines going to lines; written reports that a cast's lines already reach them.
type Local func(ctx context.Context, cmd *cli.Command, lines slog.Handler) (api transport.Endpoint, written bool, stop func(), err error)

// clients is castor's public API, dialed.
type clients struct {
	casts   castorv1connect.CastServiceClient
	devices castorv1connect.DeviceServiceClient
}

// dial reaches the API cfg names, or the one local runs, and settles which of a cast's lines to show; release stops what local runs.
func dial(ctx context.Context, cmd *cli.Command, cfg *Config, local Local) (_ clients, lines cast.Lines, release func(), err error) {
	debug := process.Debug(cmd)
	lines = cast.LinesWarn
	if debug {
		lines = cast.LinesDebug
	}
	at, release := transport.Endpoint{URL: cfg.API.URL, Token: cfg.API.Token}, func() {}
	if at.URL == "" {
		// The servers' own lines are written here only under --debug; a cast's warnings come through its watch.
		server := slog.DiscardHandler
		if debug {
			server = cast.FromServer(slog.Default().Handler())
		}
		var written bool
		if at, written, release, err = local(ctx, cmd, server); err != nil {
			return clients{}, 0, nil, err
		}
		if debug && written {
			lines = cast.LinesNone
		}
	}
	client := at.Client()
	checked := connect.WithInterceptors(validate.NewInterceptor(validate.WithValidateResponses()))
	return clients{
		casts:   castorv1connect.NewCastServiceClient(client, at.URL, checked),
		devices: castorv1connect.NewDeviceServiceClient(client, at.URL, checked),
	}, lines, release, nil
}
