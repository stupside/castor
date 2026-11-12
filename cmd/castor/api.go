package main

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/gen/castor/v1/castorv1connect"
	"github.com/stupside/castor/internal/transport"
)

// Local runs castor's API in this process.
type Local func(ctx context.Context, cmd *cli.Command) (api transport.Endpoint, stop func(), err error)

// clients is castor's public API, dialed.
type clients struct {
	casts   castorv1connect.CastServiceClient
	devices castorv1connect.DeviceServiceClient
}

// dial reaches the API api names, or the one local runs; release stops what local runs.
func dial(ctx context.Context, cmd *cli.Command, api transport.Endpoint, local Local) (_ clients, release func(), err error) {
	at, release := api, func() {}
	if at.URL == "" {
		if at, release, err = local(ctx, cmd); err != nil {
			return clients{}, nil, err
		}
	}
	client := at.Client()
	checked := transport.Options()
	return clients{
		casts:   castorv1connect.NewCastServiceClient(client, at.URL, checked),
		devices: castorv1connect.NewDeviceServiceClient(client, at.URL, checked),
	}, release, nil
}
