package dlna

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"

	"github.com/huin/goupnp"
	"github.com/huin/goupnp/dcps/av1"

	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/media"
)

// Config holds network settings needed for DLNA device operations.
type Config struct {
	Interface *net.Interface
}

// DLNADevice implements device.Device for UPnP/DLNA media renderers.
type DLNADevice struct {
	info      device.Info
	cfg       Config
	transport *av1.AVTransport1
}

var _ device.Device = (*DLNADevice)(nil)

// NewDevice creates a DLNADevice from discovery info and config.
func NewDevice(info device.Info, cfg Config) *DLNADevice {
	return &DLNADevice{
		info: info,
		cfg:  cfg,
	}
}

func (d *DLNADevice) Connect() error {
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

func (d *DLNADevice) Play(ctx context.Context, streamURL *url.URL, contentType string) error {
	if d.transport == nil {
		return fmt.Errorf("device not connected: call Connect() first")
	}
	return d.castDirect(ctx, streamURL, contentType)
}

func (d *DLNADevice) Close() error {
	return nil
}

func (d *DLNADevice) Requirements() device.Requirements {
	return device.Requirements{
		SupportedContentTypes: []string{
			"video/mp2t",
			media.MP4,
		},
	}
}

func (d *DLNADevice) castDirect(ctx context.Context, streamURL *url.URL, contentType string) error {
	metadata, err := buildDIDLMetadata(streamURL, contentType)
	if err != nil {
		return fmt.Errorf("building DIDL-Lite metadata: %w", err)
	}

	slog.Debug("setting AVTransport URI", "url", streamURL.String(), "metadata", metadata)

	if err := d.transport.SetAVTransportURICtx(ctx, 0, streamURL.String(), metadata); err != nil {
		return fmt.Errorf("setting transport URI: %w", err)
	}

	slog.Debug("sending Play command")

	if err := d.transport.PlayCtx(ctx, 0, "1"); err != nil {
		return fmt.Errorf("starting playback: %w", err)
	}
	return nil
}

