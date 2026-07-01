// Package device discovers media renderers on the local network and speaks
// their control protocols (DLNA/UPnP AVTransport, Chromecast). The Device
// interface is deliberately small: the cast pipeline decides what to send
// (subtitles are burned in upstream), a Device only needs to fetch and play.
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

// Device is a connected renderer, ready to play. Obtain one via Connect.
type Device interface {
	// Play points the renderer at streamURL, advertised as contentType.
	Play(ctx context.Context, streamURL *url.URL, contentType string) error

	// SupportedContentTypes lists MIME types the renderer accepts directly
	// (pass-through without transcoding).
	SupportedContentTypes() []string

	// StreamHeaders returns protocol-specific HTTP headers the local stream
	// server must send when this renderer fetches contentType. Nil when the
	// protocol needs none.
	StreamHeaders(contentType string) map[string]string

	// Close releases the connection to the renderer.
	Close() error
}

// Connect establishes a control connection to the device described by info.
func Connect(ctx context.Context, info Info) (Device, error) {
	switch info.Type {
	case TypeDLNA:
		return connectDLNA(ctx, info)
	case TypeChromecast:
		return connectChromecast(info)
	}
	return nil, fmt.Errorf("unknown device type: %q", info.Type)
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

// Discover scans the local network for DLNA renderers. (Chromecast discovery
// requires mDNS which the current dependency set doesn't ship; callers
// connect to known Chromecast addresses directly.)
func Discover(ctx context.Context, timeout time.Duration) ([]Info, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	results, err := goupnp.DiscoverDevicesCtx(ctx, "urn:schemas-upnp-org:device:MediaRenderer:1")
	if err != nil {
		slog.WarnContext(ctx, "DLNA discovery error", "error", err)
		return nil, nil
	}

	devices := make([]Info, 0, len(results))
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
