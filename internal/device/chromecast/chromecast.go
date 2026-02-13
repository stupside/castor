package chromecast

import (
	"context"
	"fmt"
	"net/url"

	"github.com/vishen/go-chromecast/application"

	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/media"
)

const defaultPort = 8009

// Device implements device.Device for Google Chromecast devices.
type Device struct {
	app  *application.Application
	info device.Info
}

var _ device.Device = (*Device)(nil)

// NewDevice creates a Device from discovery info.
func NewDevice(info device.Info) *Device {
	return &Device{
		info: info,
	}
}

func (c *Device) Connect() error {
	c.app = application.NewApplication(
		application.WithCacheDisabled(true),
	)
	if err := c.app.Start(c.info.Address, defaultPort); err != nil {
		return fmt.Errorf("connecting to chromecast: %w", err)
	}

	return nil
}

func (c *Device) Play(ctx context.Context, streamURL *url.URL, contentType string) error {
	if err := c.app.Load(streamURL.String(), 0, contentType, false, true, true); err != nil {
		return fmt.Errorf("starting chromecast playback: %w", err)
	}
	return nil
}

func (c *Device) Close() error {
	if c.app != nil {
		return c.app.Close(false)
	}
	return nil
}

func (c *Device) SupportedContentTypes() []string {
	return []string{media.HLS, media.MP4, media.MKV, media.WebM}
}
