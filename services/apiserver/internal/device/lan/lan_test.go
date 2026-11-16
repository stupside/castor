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
		if !slices.ContainsFunc(iface.Addresses, func(a netip.Addr) bool { return !a.IsLinkLocalUnicast() }) {
			t.Errorf("%s is listed with only link-local addresses %v", iface.Name, iface.Addresses)
		}
	}
}
