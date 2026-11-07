// Package chromecast casts to Google Cast receivers.
package chromecast

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"

	castmedia "github.com/vishen/go-chromecast/cast"
	castdns "github.com/vishen/go-chromecast/dns"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
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
	ch, err := dial(dialCtx, device.WithPort(info.Address, strconv.Itoa(castPort)), dev.watchMessage)
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

// Capabilities are the family's own, so describing a receiver neither dials nor wakes it.
func (Family) Capabilities(context.Context, device.Info) (*mediav1.Capabilities, error) {
	return capabilities(), nil
}

// Locate passes the address straight through, Connect already accepting both of its forms.
func (Family) Locate(_ context.Context, address string) (string, error) {
	return address, nil
}

// Discover browses mDNS (_googlecast._tcp) until ctx expires.
func (Family) Discover(ctx context.Context) []device.Info {
	interfaces, err := net.Interfaces()
	if err != nil {
		slog.WarnContext(ctx, "chromecast interfaces", "error", err)
		return nil
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var devices []device.Info
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		wg.Go(func() {
			entries, err := castdns.DiscoverCastDNSEntries(ctx, &iface)
			if err != nil {
				slog.DebugContext(ctx, "chromecast discovery", "interface", iface.Name, "error", err)
				return
			}
			for entry := range entries {
				if found, ok := infoOnInterface(entry, iface.Name); ok {
					mu.Lock()
					devices = append(devices, found)
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	return devices
}

func infoOnInterface(entry castdns.CastEntry, zone string) (device.Info, bool) {
	var host string
	switch {
	case entry.AddrV4 != nil:
		host = entry.AddrV4.String()
	case entry.AddrV6 != nil:
		host = entry.AddrV6.String()
		if entry.AddrV6.IsLinkLocalUnicast() {
			if zone == "" {
				return device.Info{}, false
			}
			host += "%" + zone
		}
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
