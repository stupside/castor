package mediaserver

import (
	"net/netip"
	"testing"
)

func TestReachableByDevices(t *testing.T) {
	tests := []struct {
		name string
		held []string
		want string
	}{
		{"lan IPv4", []string{"192.168.1.20", "2a01:cb0a:8001:c063::1"}, "192.168.1.20"},
		{"CLAT IPv4 only translates, so the IPv6 address is the one", []string{"192.0.0.2", "fe80::1", "2a01:cb0a:8001:c063:2dd0::1"}, "2a01:cb0a:8001:c063:2dd0::1"},
		{"link-local addresses never reach a device", []string{"169.254.3.4", "fe80::1"}, ""},
		{"nothing held", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var held []netip.Addr
			for _, s := range tt.held {
				held = append(held, netip.MustParseAddr(s))
			}
			got, ok := reachableByDevices(held)
			if ok != (tt.want != "") || (ok && got.String() != tt.want) {
				t.Fatalf("reachableByDevices(%v) = %v, %v; want %q", tt.held, got, ok, tt.want)
			}
		})
	}
}
