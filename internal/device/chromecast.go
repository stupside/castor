package device

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"

	"github.com/vishen/go-chromecast/application"
	castdns "github.com/vishen/go-chromecast/dns"

	"github.com/stupside/castor/internal/media"
)

const chromecastPort = 8009

type chromecastDevice struct {
	app *application.Application
}

var _ Device = (*chromecastDevice)(nil)

// connectChromecast opens the cast application channel. The underlying
// library's Start has no context support, so cancellation cannot interrupt
// the dial. Address may be a bare host (default port 8009) or host:port as
// produced by discovery; cast groups advertise on non-default ports.
func connectChromecast(info Info) (Device, error) {
	host := info.Address
	port := chromecastPort
	if h, p, err := net.SplitHostPort(info.Address); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			host, port = h, n
		}
	}

	app := application.NewApplication(
		application.WithCacheDisabled(true),
	)
	if err := app.Start(host, port); err != nil {
		return nil, fmt.Errorf("connecting to chromecast: %w", err)
	}
	return &chromecastDevice{app: app}, nil
}

// discoverChromecast browses mDNS (_googlecast._tcp) for cast devices until
// ctx expires. The entries channel is closed by the library when ctx is done,
// so ranging over it consumes the full discovery window.
func discoverChromecast(ctx context.Context) []Info {
	entries, err := castdns.DiscoverCastDNSEntries(ctx, nil)
	if err != nil {
		slog.WarnContext(ctx, "chromecast discovery error", "error", err)
		return nil
	}

	var devices []Info
	seen := make(map[string]struct{})
	for entry := range entries {
		info, ok := chromecastInfo(entry)
		if !ok {
			continue
		}

		// mDNS re-announces the same device; dedupe by UUID, falling back
		// to the resolved address when the entry carries no UUID.
		key := cmp.Or(entry.UUID, info.Address)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		devices = append(devices, info)
	}
	return devices
}

// chromecastInfo maps an mDNS cast entry to a device Info, reporting false when
// the entry advertises no usable IP address. IPv4 is preferred over IPv6, and
// the friendly DeviceName over the mDNS instance and host names. A non-default
// port is preserved as host:port so cast groups, which advertise on random high
// ports rather than 8009, stay connectable; a bare host implies port 8009.
func chromecastInfo(entry castdns.CastEntry) (Info, bool) {
	var host string
	switch {
	case entry.AddrV4 != nil:
		host = entry.AddrV4.String()
	case entry.AddrV6 != nil:
		host = entry.AddrV6.String()
	default:
		return Info{}, false
	}

	address := host
	if entry.Port > 0 && entry.Port != chromecastPort {
		address = net.JoinHostPort(host, strconv.Itoa(entry.Port))
	}

	return Info{
		Name:    cmp.Or(entry.DeviceName, entry.Name, entry.Host),
		Type:    TypeChromecast,
		Address: address,
	}, true
}

func (c *chromecastDevice) Play(_ context.Context, streamURL *url.URL, contentType string) error {
	if err := c.app.Load(streamURL.String(), 0, contentType, false, true, true); err != nil {
		return fmt.Errorf("starting chromecast playback: %w", err)
	}
	return nil
}

func (c *chromecastDevice) Close() error {
	return c.app.Close(false)
}

// chromecastCapabilities: Chromecast decides pass-through purely on the
// container (it never re-encodes video, so it carries no video envelope),
// accepting these MIME types directly.
var chromecastCapabilities = media.Renderer{
	Containers: []string{media.HLS, media.MP4, media.MKV, media.WebM},
}

// Capabilities reports Chromecast's known receiver profile. There is no runtime
// query as there is for DLNA, so this is the documented Cast media support,
// resolved from the device itself for interface parity with the DLNA path.
func (c *chromecastDevice) Capabilities() media.Renderer { return chromecastCapabilities }

// StreamHeaders returns nil: Chromecast needs no protocol-specific headers on
// the local stream server's responses.
func (c *chromecastDevice) StreamHeaders(string) map[string]string {
	return nil
}
