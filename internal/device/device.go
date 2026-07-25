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
	TypeRoku       Type = "roku"
)

// SelfFetches reports whether a renderer of this type fetches media URLs itself
// (a smart client such as Chromecast, or Roku's channel Video node) versus only
// playing bytes castor serves to it (DLNA). It is a static property of the
// protocol, known before the network round-trip of discovery and connect, so the
// cast pipeline can decide up front that a non-self-fetching renderer will always
// serve a local spool and begin buffering the single-use source while connect runs
// concurrently. It MUST agree with the connected renderer's Capabilities().SelfFetch,
// which the copy-vs-encode stage reads once the device is in hand.
func SelfFetches(t Type) bool {
	return t == TypeChromecast || t == TypeRoku
}

type Info struct {
	Name    string
	Type    Type
	Address string
}

// Config is the resolved device target the agnostic layers carry and forward:
// which renderer to reach (Name, Type) plus an opaque Family payload. It names no
// device family, so core/cast and every other device carry it without knowing any
// one family exists. The composition root (internal/config) fills Family with the
// selected family's typed connect settings; only that family's connect reads it,
// via a type assertion in Connect. Family is nil for a family that needs none.
type Config struct {
	Name   string
	Type   Type
	Family any
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

	Close() error
}

// Connect dispatches on the device type. Each family reads its own connect
// settings out of the opaque cfg.Family (a type assertion here, in the only
// package that knows the family types); callers upstream forward cfg blindly.
func Connect(ctx context.Context, info Info, cfg Config) (Device, error) {
	switch info.Type {
	case TypeDLNA:
		return connectDLNA(ctx, info)
	case TypeChromecast:
		return connectChromecast(info)
	case TypeRoku:
		roku, _ := cfg.Family.(RokuConfig)
		return connectRoku(ctx, info, roku)
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

// Discover scans the local network for renderers: DLNA via SSDP (MediaRenderer),
// Chromecast via mDNS (_googlecast._tcp), and Roku via SSDP (roku:ecp). All scans
// run in parallel and share the same timeout window; a protocol that fails
// contributes no devices rather than failing the whole scan.
func Discover(ctx context.Context, timeout time.Duration) ([]Info, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var dlna, chromecast, roku []Info
	var wg sync.WaitGroup
	wg.Go(func() { dlna = discoverDLNA(ctx) })
	wg.Go(func() { chromecast = discoverChromecast(ctx) })
	wg.Go(func() { roku = discoverRoku(ctx) })
	wg.Wait()

	return slices.Concat(dlna, chromecast, roku), nil
}
