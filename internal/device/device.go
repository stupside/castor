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
	castdns "github.com/vishen/go-chromecast/dns"
)

type Type string

const (
	TypeDLNA       Type = "dlna"
	TypeChromecast Type = "chromecast"
)

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

	Close() error
}

func Connect(ctx context.Context, info Info) (Device, error) {
	switch info.Type {
	case TypeDLNA:
		return connectDLNA(ctx, info)
	case TypeChromecast:
		return connectChromecast(info)
	}
	return nil, fmt.Errorf("unknown device type: %q", info.Type)
}

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

// Discover scans the local network for DLNA and Chromecast renderers.
func Discover(ctx context.Context, timeout time.Duration) ([]Info, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type result struct {
		kind    Type
		devices []Info
		err     error
	}
	results := make(chan result, 2)

	go func() {
		devices, err := discoverDLNA(ctx)
		results <- result{kind: TypeDLNA, devices: devices, err: err}
	}()
	go func() {
		devices, err := discoverChromecasts(ctx)
		results <- result{kind: TypeChromecast, devices: devices, err: err}
	}()

	var devices []Info
	for range 2 {
		result := <-results
		if result.err != nil {
			slog.WarnContext(ctx, "device discovery error", "type", result.kind, "error", result.err)
			continue
		}
		devices = append(devices, result.devices...)
	}
	return devices, nil
}

func discoverDLNA(ctx context.Context) ([]Info, error) {
	results, err := goupnp.DiscoverDevicesCtx(ctx, "urn:schemas-upnp-org:device:MediaRenderer:1")
	if err != nil {
		return nil, err
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

func discoverChromecasts(ctx context.Context) ([]Info, error) {
	entries, err := castdns.DiscoverCastDNSEntries(ctx, nil)
	if err != nil {
		return nil, err
	}

	var devices []Info
	for entry := range entries {
		info, ok := chromecastInfo(entry)
		if !ok {
			continue
		}
		devices = append(devices, info)
	}
	return devices, nil
}

func chromecastInfo(entry castdns.CastEntry) (Info, bool) {
	if entry.DeviceName == "" || entry.AddrV4 == nil {
		return Info{}, false
	}
	return Info{
		Name:    entry.DeviceName,
		Type:    TypeChromecast,
		Address: entry.AddrV4.String(),
	}, true
}
