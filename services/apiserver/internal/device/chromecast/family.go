// Package chromecast casts to Google Cast receivers.
package chromecast

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"

	castmedia "github.com/vishen/go-chromecast/cast"
	castdns "github.com/vishen/go-chromecast/dns"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

const (
	castPort = 8009

	defaultMediaReceiver = "CC1AD845"
)

// Family is the Cast strategy.
type Family struct{}

// familyType is the type the contract names this family by.
const familyType device.Type = "chromecast"

func (Family) Type() device.Type { return familyType }

var _ device.Family = Family{}

func (Family) Connect(ctx context.Context, info device.Info) (device.Device, error) {
	dev := &session{name: cmp.Or(info.Name, info.Address), ending: newEnding()}
	dialCtx, cancel := context.WithTimeout(ctx, answerWithin)
	defer cancel()
	ch, err := dial(dialCtx, dialAddress(info.Address), dev.watchMessage)
	if err != nil {
		return nil, fmt.Errorf("connecting to chromecast: %w", err)
	}
	dev.ch = ch
	if err := ch.send(ctx, receiverID, nsConnection, &castmedia.PayloadHeader{Type: msgConnect}); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("connecting to chromecast: %w", err)
	}
	if _, err := dev.receiverStatus(ctx, &castmedia.PayloadHeader{Type: msgGetStatus}); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("connecting to chromecast: %w", err)
	}
	return dev, nil
}

// dialAddress completes a bare host with the Cast port.
func dialAddress(address string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(strings.Trim(address, "[]"), strconv.Itoa(castPort))
}

// Locate passes the address straight through, Connect already accepting both of its forms.
func (Family) Locate(_ context.Context, address string) (string, error) {
	return address, nil
}

// Discover browses mDNS (_googlecast._tcp) until ctx expires.
func (Family) Discover(ctx context.Context) []device.Info {
	entries, err := castdns.DiscoverCastDNSEntries(ctx, nil)
	if err != nil {
		slog.WarnContext(ctx, "chromecast discovery error", "error", err)
		return nil
	}

	var devices []device.Info
	for entry := range entries {
		if info, ok := info(entry); ok {
			devices = append(devices, info)
		}
	}
	return devices
}

// info reports false when the entry advertises no usable IP address.
func info(entry castdns.CastEntry) (device.Info, bool) {
	var host string
	switch {
	case entry.AddrV4 != nil:
		host = entry.AddrV4.String()
	case entry.AddrV6 != nil:
		host = entry.AddrV6.String()
	default:
		return device.Info{}, false
	}

	address := host
	if entry.Port > 0 && entry.Port != castPort {
		address = net.JoinHostPort(host, strconv.Itoa(entry.Port))
	}

	return device.Info{
		ID:      cmp.Or(entry.UUID, address),
		Name:    cmp.Or(entry.DeviceName, entry.Name, entry.Host),
		Type:    familyType,
		Address: address,
	}, true
}
