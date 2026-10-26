// Package mediaclient is the API server's link to the media server: its casts, its rankings, and the drive that lends it a device.
package mediaclient

import (
	"context"
	"errors"
	"net/http"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/gen/castor/media/v1/mediav1connect"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/transport"
)

// Client is the media server's services, as the API server uses them.
type Client struct {
	casts   mediav1connect.CastServiceClient
	devices mediav1connect.DeviceServiceClient
	streams mediav1connect.StreamServiceClient
}

// New reaches the media server at baseURL through client, which carries whatever credentials it asks.
func New(client *http.Client, baseURL string) *Client {
	valid := transport.Options()
	return &Client{
		casts:   mediav1connect.NewCastServiceClient(client, baseURL, valid),
		devices: mediav1connect.NewDeviceServiceClient(client, baseURL, valid),
		streams: mediav1connect.NewStreamServiceClient(client, baseURL, valid),
	}
}

// Start begins cast id of source as asked.
func (c *Client) Start(ctx context.Context, id string, source *mediav1.Source, asked *mediav1.PlaybackSettings, start time.Duration) error {
	req := &mediav1.StartRequest{CastId: id, Source: source, Settings: asked}
	if start > 0 {
		req.Start = durationpb.New(start)
	}
	_, err := c.casts.Start(ctx, req)
	return err
}

func (c *Client) Stop(ctx context.Context, id string) error {
	_, err := c.casts.Stop(ctx, &mediav1.StopRequest{CastId: id})
	return err
}

// Rank is the streams a cast of source as asked would walk, best first.
func (c *Client) Rank(ctx context.Context, source *mediav1.Source, asked *mediav1.PlaybackSettings) ([]*castorv1.RankedStream, error) {
	ranked, err := c.streams.Rank(ctx, &mediav1.RankRequest{Source: source, Settings: asked})
	if err != nil {
		return nil, err
	}
	return ranked.GetRanked(), nil
}

// Watch follows cast id to its end, handing status every status, and returns how it ended.
func (c *Client) Watch(ctx context.Context, id string, status func(*castorv1.CastStatus)) (*castorv1.Ended, error) {
	stream, err := c.casts.Watch(ctx, &castorv1.WatchRequest{CastId: id})
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	for stream.Receive() {
		switch u := stream.Msg().GetUpdate().(type) {
		case *castorv1.WatchResponse_Status:
			if status != nil {
				status(u.Status)
			}
		case *castorv1.WatchResponse_Ended:
			return u.Ended, nil
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("the media server's watch ended without saying how the cast did")
}
