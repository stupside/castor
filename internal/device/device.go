// Package device discovers media renderers on the local network and speaks
// their control protocols (DLNA/UPnP AVTransport, Chromecast). The Device
// interface is deliberately small: the cast pipeline decides what to send
// (subtitles are burned in upstream), a Device only needs to fetch, play,
// and report when playback ended on its side.
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

type Info struct {
	Name    string
	Type    Type
	Address string
}

// Device is a connected renderer, ready to play. Obtain one via Connect.
type Device interface {
	// Play points the renderer at streamURL, advertised as contentType.
	Play(ctx context.Context, streamURL *url.URL, contentType string) error

	// Capabilities reports what this renderer can play: the containers it
	// accepts as-is and the video envelopes it decodes natively. Each device
	// resolves this from itself at connect time (DLNA negotiates it over
	// GetProtocolInfo; Chromecast reports its known receiver profile), so the
	// copy-vs-encode decision follows what the renderer advertises rather than
	// an assumption baked in per device type.
	Capabilities() media.Renderer

	// StreamHeaders returns protocol-specific HTTP headers the local stream
	// server must send when this renderer fetches contentType. Nil when the
	// protocol needs none.
	StreamHeaders(contentType string) map[string]string

	// WaitDone blocks until the renderer reports that playback ended on its
	// side — the media finished, or the user stopped it on the device —
	// returning nil so the caller can stop streaming. It returns ctx's error
	// when ctx ends first. Valid only after Play.
	WaitDone(ctx context.Context) error

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
