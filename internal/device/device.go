package device

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/huin/goupnp"
)

// Type identifies the kind of casting device.
type Type string

const (
	TypeDLNA       Type = "dlna"
	TypeChromecast Type = "chromecast"
)

// Info holds discovery information about a device.
type Info struct {
	Name    string
	Type    Type
	Address string
}

// Device is the interface for all casting devices.
type Device interface {
	Play(ctx context.Context, streamURL *url.URL, contentType string) error
	Close() error
	Connect() error
	SupportedContentTypes() []string
}

// FindInfo discovers a specific device by type and name on the network.
func FindInfo(ctx context.Context, timeout time.Duration, dtype Type, name string) (Info, error) {
	devices, err := Discover(ctx, timeout)
	if err != nil {
		return Info{}, err
	}

	for _, d := range devices {
		if d.Type == dtype && strings.EqualFold(d.Name, name) {
			return d, nil
		}
	}

	return Info{}, fmt.Errorf("device %q (type %s) not found", name, dtype)
}

// Discover scans the local network for DLNA and Chromecast devices.
func Discover(ctx context.Context, timeout time.Duration) ([]Info, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var devices []Info

	dlnaDevices, err := discoverDLNA(ctx)
	if err != nil {
		slog.Warn("DLNA discovery error", "error", err)
	}
	devices = append(devices, dlnaDevices...)

	return devices, nil
}

func discoverDLNA(ctx context.Context) ([]Info, error) {
	results, err := goupnp.DiscoverDevicesCtx(ctx, "urn:schemas-upnp-org:device:MediaRenderer:1")
	if err != nil {
		return nil, fmt.Errorf("SSDP discovery: %w", err)
	}

	var devices []Info
	for _, r := range results {
		if r.Root == nil {
			continue
		}
		devices = append(devices, Info{
			Name:    r.Root.Device.FriendlyName,
			Type:    TypeDLNA,
			Address: r.Location.String(),
		})
	}
	return devices, nil
}
