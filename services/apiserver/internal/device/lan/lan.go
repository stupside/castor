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
	// Addresses each carry the length of the network the interface holds the address in.
	Addresses []netip.Prefix
}

// OnLink is whether address is in a network the interface holds: a neighbour, where anything else is behind a gateway.
func (i Interface) OnLink(address netip.Addr) bool {
	return slices.ContainsFunc(i.Addresses, func(held netip.Prefix) bool { return held.Contains(address) })
}

// prefixOf is the address of network with the length of its mask, false for a mask that is not a prefix.
func prefixOf(network *net.IPNet) (netip.Prefix, bool) {
	address, ok := netip.AddrFromSlice(network.IP)
	ones, bits := network.Mask.Size()
	if !ok || bits == 0 {
		return netip.Prefix{}, false
	}
	address = address.Unmap()
	if address.Is4() && bits == 128 {
		// A four-byte address held under a sixteen-byte mask.
		ones -= 96
	}
	prefix := netip.PrefixFrom(address, ones)
	return prefix, prefix.IsValid()
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
		var addresses []netip.Prefix
		for _, address := range found {
			if network, ok := address.(*net.IPNet); ok {
				if prefix, ok := prefixOf(network); ok {
					addresses = append(addresses, prefix)
				}
			}
		}
		if slices.ContainsFunc(addresses, func(a netip.Prefix) bool { return !a.Addr().IsLinkLocalUnicast() }) {
			reached = append(reached, Interface{Interface: iface, Addresses: addresses})
		}
	}
	return reached, nil
}
