// Package fcast casts to FCast receivers over protocol v3.
package fcast

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/grandcat/zeroconf"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

const (
	defaultPort = 46899
	serviceType = "_fcast._tcp"
)

// Family is the FCast strategy.
type Family struct{}

// familyType is the type the contract names this family by.
const familyType device.Type = "fcast"

func (Family) Type() device.Type { return familyType }

var _ device.Family = Family{}

func (Family) Connect(ctx context.Context, info device.Info) (device.Device, error) {
	dialCtx, cancel := context.WithTimeout(ctx, answerWithin)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "tcp", device.WithPort(info.Address, strconv.Itoa(defaultPort)))
	if err != nil {
		return nil, fmt.Errorf("connecting to fcast receiver: %w", err)
	}
	return open(ctx, conn, cmp.Or(info.Name, info.Address))
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

// named is a host, with its port only where it is not FCast's default.
func named(host string, port int) string {
	if port > 0 && port != defaultPort {
		return net.JoinHostPort(host, strconv.Itoa(port))
	}
	return host
}

// Discover browses mDNS (_fcast._tcp) until ctx expires.
func (Family) Discover(ctx context.Context) []device.Info {
	interfaces, err := net.Interfaces()
	if err != nil {
		slog.WarnContext(ctx, "fcast interfaces", "error", err)
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
			resolver, err := zeroconf.NewResolver(zeroconf.SelectIPTraffic(zeroconf.IPv4), zeroconf.SelectIfaces([]net.Interface{iface}))
			if err != nil {
				slog.DebugContext(ctx, "fcast discovery", "interface", iface.Name, "error", err)
				return
			}
			entries := make(chan *zeroconf.ServiceEntry)
			if err := resolver.Browse(ctx, serviceType, "local", entries); err != nil {
				slog.DebugContext(ctx, "fcast discovery", "interface", iface.Name, "error", err)
				return
			}
			// The resolver closes entries once ctx ends.
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

// infoOnInterface names an announced receiver by its instance; FCast announces no stable identity, so its ID adds where it answers.
func infoOnInterface(entry *zeroconf.ServiceEntry, zone string) (device.Info, bool) {
	var host string
	switch {
	case len(entry.AddrIPv4) > 0:
		host = entry.AddrIPv4[0].String()
	case len(entry.AddrIPv6) > 0:
		host = entry.AddrIPv6[0].String()
		if entry.AddrIPv6[0].IsLinkLocalUnicast() {
			if zone == "" {
				return device.Info{}, false
			}
			host += "%" + zone
		}
	default:
		return device.Info{}, false
	}

	address := named(host, entry.Port)

	name := unescapeLabel(entry.Instance)
	return device.Info{ID: name + "@" + address, Name: name, Type: familyType, Address: address}, true
}

// unescapeLabel reads a DNS label as presentation format writes it: \DDD is a decimal byte, \X the character X.
func unescapeLabel(label string) string {
	var b strings.Builder
	for i := 0; i < len(label); i++ {
		if label[i] != '\\' || i+1 == len(label) {
			b.WriteByte(label[i])
			continue
		}
		if i+3 < len(label) {
			if n, err := strconv.ParseUint(label[i+1:i+4], 10, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(label[i+1])
		i++
	}
	return b.String()
}
