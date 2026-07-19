package device

import (
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
		var host string
		switch {
		case entry.AddrV4 != nil:
			host = entry.AddrV4.String()
		case entry.AddrV6 != nil:
			host = entry.AddrV6.String()
		default:
			continue
		}

		key := entry.UUID
		if key == "" {
			key = host + ":" + strconv.Itoa(entry.Port)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		name := entry.DeviceName
		if name == "" {
			name = entry.Name
		}
		if name == "" {
			name = entry.Host
		}

		address := host
		if entry.Port > 0 && entry.Port != chromecastPort {
			address = net.JoinHostPort(host, strconv.Itoa(entry.Port))
		}

		devices = append(devices, Info{
			Name:    name,
			Type:    TypeChromecast,
			Address: address,
		})
	}
	return devices
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

func (c *chromecastDevice) SupportedContentTypes() []string {
	return []string{media.HLS, media.MP4, media.MKV, media.WebM}
}

// StreamHeaders returns nil: Chromecast needs no protocol-specific headers on
// the local stream server's responses.
func (c *chromecastDevice) StreamHeaders(string) map[string]string {
	return nil
}
