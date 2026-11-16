// Package lan lists the network interfaces discovery speaks on.
package lan

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
)

// Interface is a network interface that reaches a LAN, with every address it holds.
type Interface struct {
	net.Interface
	Addresses []netip.Addr
}

// Interfaces are those up and able to multicast with an address beyond link-local: not loopback, not a tunnel with no neighbours.
func Interfaces() ([]Interface, error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("listing interfaces: %w", err)
	}
	var reached []Interface
	for _, iface := range all {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		found, err := iface.Addrs()
		if err != nil {
			slog.Debug("reading an interface's addresses", "interface", iface.Name, "error", err)
			continue
		}
		var addresses []netip.Addr
		for _, address := range found {
			if network, ok := address.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(network.IP); ok {
					addresses = append(addresses, ip.Unmap())
				}
			}
		}
		if slices.ContainsFunc(addresses, func(a netip.Addr) bool { return !a.IsLinkLocalUnicast() }) {
			reached = append(reached, Interface{Interface: iface, Addresses: addresses})
		}
	}
	return reached, nil
}
