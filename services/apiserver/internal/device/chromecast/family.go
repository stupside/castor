// Package chromecast casts to Google Cast receivers.
package chromecast

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"sync"

	castmedia "github.com/vishen/go-chromecast/cast"

	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/device/lan"
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
	dev := &session{name: cmp.Or(info.Name, info.Address)}
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

// Locate names a pinned device as discovery does, so both lease the same endpoint.
func (Family) Locate(_ context.Context, address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return address, nil
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return "", fmt.Errorf("port %q: %w", port, err)
	}
	return named(host, n), nil
}

// named is a host, with its port only where it is not the cast port.
func named(host string, port int) string {
	if port > 0 && port != castPort {
		return net.JoinHostPort(host, strconv.Itoa(port))
	}
	return host
}

// Discover browses mDNS on every LAN interface until ctx expires.
func (Family) Discover(ctx context.Context) []device.Info {
	interfaces, err := lan.Interfaces()
	if err != nil {
		slog.WarnContext(ctx, "chromecast interfaces", "error", err)
		return nil
	}
	found := make([][]device.Info, len(interfaces))
	var wg sync.WaitGroup
	for i, iface := range interfaces {
		wg.Go(func() { found[i] = discoverOn(ctx, iface) })
	}
	wg.Wait()
	return slices.Concat(found...)
}
