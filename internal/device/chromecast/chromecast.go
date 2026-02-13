package chromecast

import (
	"context"
	"fmt"
	"net/url"

	"github.com/vishen/go-chromecast/application"

	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/media"
)

// ChromecastDevice implements device.Device for Google Chromecast devices.
type ChromecastDevice struct {
	port uint
	app  *application.Application
	info device.Info
}

var _ device.Device = (*ChromecastDevice)(nil)

// NewDevice creates a ChromecastDevice from discovery info.
func NewDevice(info device.Info) *ChromecastDevice {
	return &ChromecastDevice{
		info: info,
		port: 8009,
	}
}

func (c *ChromecastDevice) Connect() error {
	c.app = application.NewApplication(
		application.WithDebug(false),
		application.WithCacheDisabled(true),
	)
	if err := c.app.Start(c.info.Address, int(c.port)); err != nil {
		return fmt.Errorf("connecting to chromecast: %w", err)
	}
	return nil
}

func (c *ChromecastDevice) Play(ctx context.Context, streamURL *url.URL, contentType string) error {
	return c.app.Load(streamURL.String(), 0, contentType, false, true, true)
}

func (c *ChromecastDevice) Close() error {
	if c.app != nil {
		return c.app.Close(false)
	}
	return nil
}

func (c *ChromecastDevice) Requirements() device.Requirements {
	return device.Requirements{
		SupportedContentTypes: []string{
			media.HLS,
			media.MP4,
			media.MKV,
			media.WebM,
		},
	}
}
