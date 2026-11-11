package fcast

import (
	"net"
	"testing"

	"github.com/grandcat/zeroconf"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

func TestTheFamilyIsTheContractsFCast(t *testing.T) {
	if got := (device.Info{ID: "tv", Type: Family{}.Type()}).Public().GetType(); got != castorv1.DeviceType_DEVICE_TYPE_FCAST {
		t.Errorf("an FCast receiver is listed as %v", got)
	}
}

func TestAnAnnouncedReceiverIsDialledWhereItListens(t *testing.T) {
	announced := func(port int, v4, v6 string) *zeroconf.ServiceEntry {
		entry := zeroconf.NewServiceEntry(`FCast-Sony-BRAVIA\ 4K`, serviceType, "local")
		entry.Port = port
		if v4 != "" {
			entry.AddrIPv4 = []net.IP{net.ParseIP(v4)}
		}
		if v6 != "" {
			entry.AddrIPv6 = []net.IP{net.ParseIP(v6)}
		}
		return entry
	}
	for _, tt := range []struct {
		name  string
		entry *zeroconf.ServiceEntry
		zone  string
		want  string
		ok    bool
	}{
		{"IPv4 on the default port", announced(defaultPort, "192.0.2.7", "fe80::7"), "en0", "192.0.2.7", true},
		{"another port", announced(46900, "192.0.2.7", ""), "en0", "192.0.2.7:46900", true},
		{"link-local IPv6 on the interface it was heard on", announced(defaultPort, "", "fe80::7"), "en0", "fe80::7%en0", true},
		{"link-local IPv6 with no interface", announced(defaultPort, "", "fe80::7"), "", "", false},
		{"no address", announced(defaultPort, "", ""), "en0", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := infoOnInterface(tt.entry, tt.zone)
			if ok != tt.ok || got.Address != tt.want {
				t.Fatalf("info = %+v, %v; want address %q, %v", got, ok, tt.want, tt.ok)
			}
			if ok && (got.Name != "FCast-Sony-BRAVIA 4K" || got.ID != got.Name+"@"+tt.want || got.Type != familyType) {
				t.Errorf("info = %+v, want the instance it announced as its name, and where it answers in its identity", got)
			}
		})
	}
}

func TestReceiversSharingADisplayNameAreTwoDevices(t *testing.T) {
	living, _ := infoOnInterface(&zeroconf.ServiceEntry{Instance: "FCast-Sony-BRAVIA", Port: defaultPort, AddrIPv4: []net.IP{net.ParseIP("192.0.2.7")}}, "en0")
	bedroom, _ := infoOnInterface(&zeroconf.ServiceEntry{Instance: "FCast-Sony-BRAVIA", Port: defaultPort, AddrIPv4: []net.IP{net.ParseIP("192.0.2.8")}}, "en1")
	if living.ID == bedroom.ID {
		t.Errorf("two receivers announcing %q share the ID %q, so casting to one can reach the other", living.Name, living.ID)
	}
}

func TestAnInstanceIsReadAsDNSWritesIt(t *testing.T) {
	for label, want := range map[string]string{
		`Living\ Room`:      "Living Room",
		`Caf\195\169`:       "Café",
		`v2\.1`:             "v2.1",
		`back\\slash`:       `back\slash`,
		`not\9a\ number`:    "not9a number",
		`trailing\`:         `trailing\`,
		`FCast-Sony-BRAVIA`: "FCast-Sony-BRAVIA",
	} {
		if got := unescapeLabel(label); got != want {
			t.Errorf("unescapeLabel(%q) = %q, want %q", label, got, want)
		}
	}
}

func TestAPinnedAddressIsNamedAsDiscoveryNamesIt(t *testing.T) {
	for pinned, want := range map[string]string{
		"192.168.1.5:46899": "192.168.1.5",
		"192.168.1.5:46900": "192.168.1.5:46900",
		"192.168.1.5":       "192.168.1.5",
		"[fe80::1]:46899":   "fe80::1",
	} {
		if got, err := (Family{}).Locate(t.Context(), pinned); err != nil || got != want {
			t.Errorf("Locate(%q) = %q, %v; want %q", pinned, got, err, want)
		}
	}
}
