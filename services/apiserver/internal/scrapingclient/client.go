// Package scrapingclient connects the API server to page resolution only.
package scrapingclient

import (
	"context"
	"net/http"

	scrapingv1 "github.com/stupside/castor/gen/castor/scraping/v1"
	"github.com/stupside/castor/gen/castor/scraping/v1/scrapingv1connect"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/transport"
)

type Client struct {
	service scrapingv1connect.ScrapingServiceClient
}

func New(client *http.Client, url string) *Client {
	valid, named := transport.Options(), transport.Peering(transport.Peer{Name: "the scraping server", TokenKey: "servers.scraping.token"})
	return &Client{service: scrapingv1connect.NewScrapingServiceClient(client, url, valid, named)}
}
func (c *Client) ResolvePages(ctx context.Context, urls []string) ([]*castorv1.StreamCandidate, error) {
	response, err := c.service.Resolve(ctx, &scrapingv1.ResolveRequest{Urls: urls})
	if err != nil {
		return nil, err
	}
	return response.GetStreams(), nil
}
