package mediaserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
)

// clat is the IPv4 range a CLAT translator gives this machine on an IPv6-mostly network: it never leaves the machine.
var clat = netip.MustParsePrefix("192.0.0.0/24")

// deviceListener opens where devices on this machine's network reach its embedded media server.
func (c *Config) deviceListener(ctx context.Context) (net.Listener, error) {
	ip, err := lanIP(ctx, c.Servers.Media.Network.Interface)
	if err != nil {
		return nil, err
	}
	return net.Listen("tcp", net.JoinHostPort(ip, "0"))
}

// advertised is where devices reach a server listening on l: servers.media.advertise, or l's port on this machine's network.
func (c *Config) advertised(ctx context.Context, l net.Listener) (*url.URL, error) {
	if c.Servers.Media.Advertise != "" {
		return url.Parse(c.Servers.Media.Advertise)
	}
	ip, err := lanIP(ctx, c.Servers.Media.Network.Interface)
	if err != nil {
		return nil, err
	}
	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		return nil, err
	}
	return &url.URL{Scheme: "http", Host: net.JoinHostPort(ip, port)}, nil
}

// lanIP is this machine's address on its network: the pinned interface's, or the default route's.
func lanIP(ctx context.Context, iface string) (string, error) {
	var held []netip.Addr
	if iface == "" {
		var err error
		if held, err = defaultRouteAddrs(ctx); err != nil {
			return "", fmt.Errorf("detecting default-route address (set servers.media.network.interface to pin one): %w", err)
		}
	} else {
		pinned, err := net.InterfaceByName(iface)
		if err != nil {
			return "", fmt.Errorf("looking up interface %q: %w", iface, err)
		}
		addrs, err := pinned.Addrs()
		if err != nil {
			return "", fmt.Errorf("listing addresses on %s: %w", iface, err)
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(ipNet.IP); ok {
					held = append(held, ip.Unmap())
				}
			}
		}
	}
	if ip, ok := reachableByDevices(held); ok {
		return ip.String(), nil
	}
	return "", fmt.Errorf("none of %v is an address devices can reach", held)
}

// defaultRouteAddrs are the addresses this machine sends from towards the internet, over IPv4 and over IPv6.
func defaultRouteAddrs(ctx context.Context) ([]netip.Addr, error) {
	var held []netip.Addr
	var failed []error
	for _, probe := range []struct{ network, target string }{{"udp4", "8.8.8.8:53"}, {"udp6", "[2001:4860:4860::8888]:53"}} {
		conn, err := (&net.Dialer{}).DialContext(ctx, probe.network, probe.target)
		if err != nil {
			failed = append(failed, err)
			continue
		}
		held = append(held, conn.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap())
		_ = conn.Close()
	}
	if len(held) == 0 {
		return nil, errors.Join(failed...)
	}
	return held, nil
}

// reachableByDevices is the first IPv4 address a device can reach, else the first global IPv6 one.
func reachableByDevices(held []netip.Addr) (netip.Addr, bool) {
	for _, ip := range held {
		if ip.Is4() && ip.IsGlobalUnicast() && !clat.Contains(ip) {
			return ip, true
		}
	}
	for _, ip := range held {
		if ip.Is6() && ip.IsGlobalUnicast() {
			return ip, true
		}
	}
	return netip.Addr{}, false
}
