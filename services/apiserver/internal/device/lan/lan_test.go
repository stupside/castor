package lan

import (
	"net"
	"net/netip"
	"slices"
	"testing"
)

func TestOnlyInterfacesThatReachALANAreListed(t *testing.T) {
	interfaces, err := Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			t.Errorf("%s is listed with flags %v", iface.Name, iface.Flags)
		}
		if !slices.ContainsFunc(iface.Addresses, func(a netip.Prefix) bool { return !a.Addr().IsLinkLocalUnicast() }) {
			t.Errorf("%s is listed with only link-local addresses %v", iface.Name, iface.Addresses)
		}
	}
}

func TestAnAddressIsHeldInTheNetworkItsMaskNames(t *testing.T) {
	v4 := func(ip string, mask net.IPMask) *net.IPNet { return &net.IPNet{IP: net.ParseIP(ip), Mask: mask} }
	for _, tt := range []struct {
		name    string
		network *net.IPNet
		want    string
	}{
		{"an IPv4 address under a four-byte mask", v4("192.168.1.10", net.CIDRMask(24, 32)), "192.168.1.10/24"},
		{"an IPv4 address under a sixteen-byte mask", v4("192.168.1.10", net.CIDRMask(120, 128)), "192.168.1.10/24"},
		{"a translation address held alone", v4("192.0.0.3", net.CIDRMask(32, 32)), "192.0.0.3/32"},
		{"an IPv6 address", &net.IPNet{IP: net.ParseIP("2a01:cb09:a019:c66c::1"), Mask: net.CIDRMask(64, 128)}, "2a01:cb09:a019:c66c::1/64"},
		{"a mask that is not a prefix", v4("192.168.1.10", net.IPMask{255, 0, 255, 0}), ""},
		{"an address of no length", &net.IPNet{IP: net.IP{1, 2, 3}, Mask: net.CIDRMask(24, 32)}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := prefixOf(tt.network)
			if (tt.want == "") != !ok || ok && got.String() != tt.want {
				t.Errorf("prefixOf = %v, %t; want %q", got, ok, tt.want)
			}
		})
	}
}
