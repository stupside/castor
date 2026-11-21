package chromecast

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"

	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/device/lan"
)

const (
	castService = "_googlecast._tcp"

	// browseRound is one mDNS question: the resolver asks again only after about four seconds, too late for a discovery window.
	browseRound = 1500 * time.Millisecond
)

// discoverOn finds the receivers announced on one interface, over the IP families it holds.
func discoverOn(ctx context.Context, iface lan.Interface) []device.Info {
	traffic := trafficOf(iface.Addresses)
	heard, err := browseInRounds(ctx, func(round context.Context) (<-chan *zeroconf.ServiceEntry, error) {
		return browse(round, iface.Interface, traffic)
	})
	if err != nil {
		slog.DebugContext(ctx, "chromecast discovery", "interface", iface.Name, "error", err)
	}
	var devices []device.Info
	for _, entry := range heard {
		if info, ok := infoOnInterface(entry, iface); ok {
			devices = append(devices, info)
		}
	}
	return devices
}

// trafficOf is the IP families a resolver listens on for an interface: those it has an address in, as joining one it lacks fails.
func trafficOf(addresses []netip.Prefix) zeroconf.IPType {
	var traffic zeroconf.IPType
	for _, held := range addresses {
		if held.Addr().Is4() {
			traffic |= zeroconf.IPv4
		} else {
			traffic |= zeroconf.IPv6
		}
	}
	return traffic
}

// browse asks iface's network for receivers until ctx ends, when the channel closes.
func browse(ctx context.Context, iface net.Interface, traffic zeroconf.IPType) (<-chan *zeroconf.ServiceEntry, error) {
	resolver, err := zeroconf.NewResolver(zeroconf.SelectIfaces([]net.Interface{iface}), zeroconf.SelectIPTraffic(traffic))
	if err != nil {
		return nil, fmt.Errorf("creating the mDNS resolver: %w", err)
	}
	entries := make(chan *zeroconf.ServiceEntry, 8)
	if err := resolver.Browse(ctx, castService, "local", entries); err != nil {
		return nil, fmt.Errorf("browsing %s: %w", castService, err)
	}
	return entries, nil
}

// browseInRounds asks the network again every browseRound until ctx ends, returning what any round heard.
func browseInRounds(ctx context.Context, ask func(context.Context) (<-chan *zeroconf.ServiceEntry, error)) ([]*zeroconf.ServiceEntry, error) {
	var heard []*zeroconf.ServiceEntry
	for ctx.Err() == nil {
		round, cancel := context.WithTimeout(ctx, browseRound)
		entries, err := ask(round)
		if err != nil {
			cancel()
			return heard, err
		}
		for entry := range entries {
			heard = append(heard, entry)
		}
		cancel()
	}
	return heard, nil
}

// infoOnInterface is the receiver an entry announces, reached through the interface it was heard on.
func infoOnInterface(entry *zeroconf.ServiceEntry, iface lan.Interface) (device.Info, bool) {
	txt := txtRecords(entry.Text)
	// The resolver hands over an entry once it has an address, before its TXT record: a later round hears it whole.
	if txt["id"] == "" {
		return device.Info{}, false
	}
	host, ok := receiverHost(entry, iface)
	if !ok {
		return device.Info{}, false
	}
	return device.Info{
		ID:      txt["id"],
		Name:    cmp.Or(unescape(txt["fn"]), unescape(entry.Instance), entry.HostName),
		Type:    familyType,
		Address: named(host, entry.Port),
	}, true
}

// receiverHost is the address to reach an entry at: a neighbour's before one behind a gateway, IPv4 before IPv6, link-local last.
func receiverHost(entry *zeroconf.ServiceEntry, iface lan.Interface) (string, bool) {
	var onLink4, onLink6, behind4, behind6 string
	for _, ip := range entry.AddrIPv4 {
		address, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}
		address = address.Unmap()
		if iface.OnLink(address) {
			onLink4 = cmp.Or(onLink4, address.String())
		} else {
			behind4 = cmp.Or(behind4, address.String())
		}
	}
	for _, ip := range entry.AddrIPv6 {
		if ip.IsLinkLocalUnicast() {
			continue
		}
		if address, ok := netip.AddrFromSlice(ip); ok && iface.OnLink(address) {
			onLink6 = cmp.Or(onLink6, ip.String())
		} else {
			behind6 = cmp.Or(behind6, ip.String())
		}
	}
	if host := cmp.Or(onLink4, onLink6, behind4, behind6); host != "" {
		return host, true
	}
	if iface.Name != "" && len(entry.AddrIPv6) > 0 {
		return entry.AddrIPv6[0].String() + "%" + iface.Name, true
	}
	return "", false
}

func txtRecords(text []string) map[string]string {
	records := make(map[string]string, len(text))
	for _, record := range text {
		if key, value, ok := strings.Cut(record, "="); ok {
			records[key] = value
		}
	}
	return records
}

// unescape turns the \DDD byte escapes of a DNS string back into the characters they spell.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	raw := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if b, err := strconv.ParseUint(s[i+1:i+4], 10, 8); err == nil {
				raw = append(raw, byte(b))
				i += 3
				continue
			}
		}
		raw = append(raw, s[i])
	}
	return strings.ToValidUTF8(string(raw), "�")
}
