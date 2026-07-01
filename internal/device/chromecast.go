package device

import (
	"context"
	"fmt"
	"net/url"

	"github.com/vishen/go-chromecast/application"

	"github.com/stupside/castor/internal/media"
)

const chromecastPort = 8009

// chromecastDevice implements Device for Google Chromecast devices.
type chromecastDevice struct {
	app *application.Application
}

// connectChromecast opens the cast application channel. The underlying
// library's Start has no context support, so cancellation cannot interrupt
// the dial.
func connectChromecast(info Info) (Device, error) {
	app := application.NewApplication(
		application.WithCacheDisabled(true),
	)
	if err := app.Start(info.Address, chromecastPort); err != nil {
		return nil, fmt.Errorf("connecting to chromecast: %w", err)
	}
	return &chromecastDevice{app: app}, nil
}

func (c *chromecastDevice) Play(_ context.Context, streamURL *url.URL, contentType string) error {
	if err := c.app.Load(streamURL.String(), 0, contentType, false, true, true); err != nil {
		return fmt.Errorf("starting chromecast playback: %w", err)
	}
	return nil
}

func (c *chromecastDevice) Close() error {
	return c.app.Close(false)
}

func (c *chromecastDevice) SupportedContentTypes() []string {
	return []string{media.HLS, media.MP4, media.MKV, media.WebM}
}

// StreamHeaders returns nil: Chromecast needs no protocol-specific headers on
// the local stream server's responses.
func (c *chromecastDevice) StreamHeaders(string) map[string]string {
	return nil
}
