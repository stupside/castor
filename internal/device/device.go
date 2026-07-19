// Package device discovers media renderers on the local network and speaks
// their control protocols (DLNA/UPnP AVTransport, Chromecast). The Device
// interface is deliberately small: the cast pipeline decides what to send
// (subtitles are burned in upstream), a Device only needs to fetch and play.
package device

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stupside/castor/internal/media"
)

type Type string

const (
	TypeDLNA       Type = "dlna"
	TypeChromecast Type = "chromecast"
)

// Capabilities returns what a device of type t can play: the containers it
// accepts as-is and the video envelopes it decodes natively. It is static per
// device type (both DLNA and Chromecast expose fixed profiles today), so it
// needs no connected device, which lets the DLNA planner decide copy-vs-encode
// before discovery has even finished. The per-type data lives with each device
// implementation (dlnaCapabilities, chromecastCapabilities).
func Capabilities(t Type) media.Renderer {
	switch t {
	case TypeDLNA:
		return dlnaCapabilities
	case TypeChromecast:
		return chromecastCapabilities
	}
	return media.Renderer{}
}

type Info struct {
	Name    string
	Type    Type
	Address string
}

// Device is a connected renderer, ready to play. Obtain one via Connect.
type Device interface {
	// Play points the renderer at streamURL, advertised as contentType.
	Play(ctx context.Context, streamURL *url.URL, contentType string) error

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

// Discover scans the local network for renderers: DLNA via SSDP and Chromecast
// via mDNS (_googlecast._tcp). Both scans run in parallel and share the same
// timeout window; a protocol that fails contributes no devices rather than
// failing the whole scan.
func Discover(ctx context.Context, timeout time.Duration) ([]Info, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var dlna, chromecast []Info
	var wg sync.WaitGroup
	wg.Go(func() { dlna = discoverDLNA(ctx) })
	wg.Go(func() { chromecast = discoverChromecast(ctx) })
	wg.Wait()

	return slices.Concat(dlna, chromecast), nil
}
