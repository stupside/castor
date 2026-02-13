package dlna

import (
	"context"
	"fmt"
	"net/url"

	"github.com/huin/goupnp"
	"github.com/huin/goupnp/dcps/av1"

	"github.com/stupside/castor/internal/device"
)

// Device implements device.Device for UPnP/DLNA media renderers.
type Device struct {
	info      device.Info
	transport *av1.AVTransport1
}

var _ device.Device = (*Device)(nil)

// NewDevice creates a Device from discovery info.
func NewDevice(info device.Info) *Device {
	return &Device{
		info: info,
	}
}

func (d *Device) Connect() error {
	u, err := url.Parse(d.info.Address)
	if err != nil {
		return fmt.Errorf("parsing device location URL: %w", err)
	}

	loc, err := goupnp.DeviceByURL(u)
	if err != nil {
		return fmt.Errorf("fetching device description: %w", err)
	}

	transports, err := av1.NewAVTransport1ClientsFromRootDevice(loc, u)
	if err != nil {
		return fmt.Errorf("creating AVTransport client: %w", err)
	}
	if len(transports) == 0 {
		return fmt.Errorf("no AVTransport service found on device")
	}
	d.transport = transports[0]

	return nil
}

func (d *Device) Play(ctx context.Context, streamURL *url.URL, contentType string) error {
	if d.transport == nil {
		return fmt.Errorf("device not connected: call Connect() first")
	}

	metadata, err := buildDIDLMetadata(streamURL, contentType)
	if err != nil {
		return fmt.Errorf("building DIDL-Lite metadata: %w", err)
	}

	if err := d.transport.SetAVTransportURICtx(ctx, 0, streamURL.String(), metadata); err != nil {
		return fmt.Errorf("setting transport URI: %w", err)
	}

	if err := d.transport.PlayCtx(ctx, 0, "1"); err != nil {
		return fmt.Errorf("starting playback: %w", err)
	}

	return nil
}

func (d *Device) Close() error {
	return nil
}

func (d *Device) SupportedContentTypes() []string {
	return SupportedContentTypes
}
