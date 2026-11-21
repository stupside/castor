package chromecast

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/grandcat/zeroconf"
	"github.com/stupside/castor/services/apiserver/internal/device/lan"
)

// holding is an interface named name that holds each of addresses, written with their prefix lengths such as 192.168.1.10/24.
func holding(name string, addresses ...string) lan.Interface {
	iface := lan.Interface{Name: name}
	for _, network := range addresses {
		iface.Addresses = append(iface.Addresses, netip.MustParsePrefix(network))
	}
	return iface
}

func receiver(instance string, port int, txt []string, v4 []net.IP, v6 []net.IP) *zeroconf.ServiceEntry {
	return &zeroconf.ServiceEntry{Instance: instance, Port: port, Text: txt, AddrIPv4: v4, AddrIPv6: v6}
}

func TestAReceiverWithOnlyAnIPv6AddressIsFoundByItsRoutableOne(t *testing.T) {
	entry := receiver(`BRAVIA-VH1-0ddebb36`, castPort, []string{"id=0ddebb36", "fn=Salon", "md=BRAVIA\\ VH1"}, nil,
		[]net.IP{net.ParseIP("fe80::c71:781:4379:abd8"), net.ParseIP("2a01:cb0a:8001:c063:2dd0:198d:c10c:4dbe")})
	info, ok := infoOnInterface(entry, holding("en0", "2a01:cb0a:8001:c063::1/64"))
	if !ok || info.ID != "0ddebb36" || info.Name != "Salon" || info.Address != "2a01:cb0a:8001:c063:2dd0:198d:c10c:4dbe" {
		t.Fatalf("infoOnInterface = %+v, %v; want the receiver at its global address", info, ok)
	}
}

func TestLinkLocalDiscoveryPreservesInterface(t *testing.T) {
	entry := receiver("tv", castPort, []string{"id=abc"}, nil, []net.IP{net.ParseIP("fe80::123")})
	got, ok := infoOnInterface(entry, holding("en7"))
	if !ok || got.Address != "fe80::123%en7" {
		t.Fatalf("info=%+v,%v", got, ok)
	}
	if _, ok := infoOnInterface(entry, holding("")); ok {
		t.Fatal("unscoped link-local address accepted")
	}
	entry.AddrIPv4 = []net.IP{net.ParseIP("192.0.2.1")}
	got, ok = infoOnInterface(entry, holding("en7"))
	if !ok || got.Address != "192.0.2.1" {
		t.Fatalf("IPv4 info=%+v,%v", got, ok)
	}
}

func TestAReceiverIsReachedByItsIPv4AddressWhereTheInterfaceHoldsThatNetwork(t *testing.T) {
	entry := receiver("tv", castPort, []string{"id=abc"}, []net.IP{net.ParseIP("192.168.1.50")}, []net.IP{net.ParseIP("2a01:cb0a:8001:c063::50")})
	got, ok := infoOnInterface(entry, holding("en0", "192.168.1.10/24", "2a01:cb0a:8001:c063::10/64"))
	if !ok || got.Address != "192.168.1.50" {
		t.Fatalf("info=%+v,%v, want the neighbour at its IPv4 address", got, ok)
	}
}

// An IPv6-mostly host holds only a translation address in IPv4, which reaches nothing on the LAN.
func TestAReceiverBehindTheInterfacesIPv4TranslationIsReachedByItsIPv6Address(t *testing.T) {
	entry := receiver("tv", castPort, []string{"id=abc"}, []net.IP{net.ParseIP("172.20.10.11")}, []net.IP{net.ParseIP("fe80::50"), net.ParseIP("2a01:cb09:a019:c66c:10aa:bb7f:63e5:1089")})
	got, ok := infoOnInterface(entry, holding("en0", "192.0.0.3/32", "2a01:cb09:a019:c66c:497:82dc:7103:ba0c/64"))
	if !ok || got.Address != "2a01:cb09:a019:c66c:10aa:bb7f:63e5:1089" {
		t.Fatalf("info=%+v,%v, want the receiver at its IPv6 address", got, ok)
	}
}

func TestAReceiverIsReachedByTheIPv4AddressOnTheInterfacesNetworkWhicheverIsFirst(t *testing.T) {
	entry := receiver("tv", castPort, []string{"id=abc"}, []net.IP{net.ParseIP("10.9.9.9"), net.ParseIP("192.168.1.50")}, nil)
	got, ok := infoOnInterface(entry, holding("en0", "192.168.1.10/24"))
	if !ok || got.Address != "192.168.1.50" {
		t.Fatalf("info=%+v,%v, want the address on the interface's network", got, ok)
	}
}

// The host reaches the TV over its gateway, and holds no IPv6 network its global address could be in.
func TestAReceiverBehindAGatewayIsReachedByIPv4WhereItsIPv6IsNotOnAnyNetworkTheInterfaceHolds(t *testing.T) {
	entry := receiver("tv", castPort, []string{"id=abc"}, []net.IP{net.ParseIP("10.9.9.9")}, []net.IP{net.ParseIP("2a01:cb0a:8001:c063::50")})
	got, ok := infoOnInterface(entry, holding("en0", "192.168.1.10/24", "fe80::1/64"))
	if !ok || got.Address != "10.9.9.9" {
		t.Fatalf("info=%+v,%v, want the IPv4 address over the gateway", got, ok)
	}
}

func TestAnEntryWithoutItsTXTRecordIsNotADevice(t *testing.T) {
	entry := receiver("Chromecast-abc", castPort, nil, []net.IP{net.ParseIP("192.168.1.5")}, nil)
	if _, ok := infoOnInterface(entry, holding("en0", "192.168.1.10/24")); ok {
		t.Fatal("an entry with no id was listed, under its address instead of its identity")
	}
}

func TestANameIsSpelledWithItsEscapedCharacters(t *testing.T) {
	for escaped, want := range map[string]string{
		"Salon":                      "Salon",
		`Caf\195\169`:                "Café",
		`Chromecast\032Ultra`:        "Chromecast Ultra",
		`\999 is not a byte`:         `\999 is not a byte`,
		`ends in an escape\195\169`:  "ends in an escapeé",
		`\195 alone is not a letter`: "� alone is not a letter",
	} {
		if got := unescape(escaped); got != want {
			t.Errorf("unescape(%q) = %q, want %q", escaped, got, want)
		}
	}
}

func TestAResolverListensOnlyOnTheFamiliesTheInterfaceHolds(t *testing.T) {
	for _, tt := range []struct {
		networks []string
		want     zeroconf.IPType
	}{
		{[]string{"192.168.1.2/24"}, zeroconf.IPv4},
		{[]string{"2a01:cb0a::1/64", "fe80::1/64"}, zeroconf.IPv6},
		{[]string{"192.0.0.2/32", "2a01:cb0a::1/64"}, zeroconf.IPv4AndIPv6},
	} {
		if got := trafficOf(holding("en0", tt.networks...).Addresses); got != tt.want {
			t.Errorf("trafficOf(%v) = %v, want %v", tt.networks, got, tt.want)
		}
	}
}

func TestBrowsingInRoundsHearsADeviceAFirstQuestionMissed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		asked := 0
		heard, err := browseInRounds(ctx, func(round context.Context) (<-chan *zeroconf.ServiceEntry, error) {
			asked++
			entries := make(chan *zeroconf.ServiceEntry, 1)
			go func() {
				// Only the second question gets an answer; a round ends with its context.
				if asked == 2 {
					entries <- receiver("tv", castPort, []string{"id=tv"}, nil, nil)
				}
				<-round.Done()
				close(entries)
			}()
			return entries, nil
		})
		if err != nil || len(heard) != 1 || txtRecords(heard[0].Text)["id"] != "tv" {
			t.Fatalf("heard %+v, %v; want the device the second question reached", heard, err)
		}
		if asked < 3 {
			t.Errorf("asked %d times in a 5s window, want a question every %v", asked, browseRound)
		}
	})
}
