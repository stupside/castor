package chromecast

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/grandcat/zeroconf"
)

func receiver(instance string, port int, txt []string, v4 []net.IP, v6 []net.IP) *zeroconf.ServiceEntry {
	return &zeroconf.ServiceEntry{Instance: instance, Port: port, Text: txt, AddrIPv4: v4, AddrIPv6: v6}
}

func TestAReceiverWithOnlyAnIPv6AddressIsFoundByItsRoutableOne(t *testing.T) {
	entry := receiver(`BRAVIA-VH1-0ddebb36`, castPort, []string{"id=0ddebb36", "fn=Salon", "md=BRAVIA\\ VH1"}, nil,
		[]net.IP{net.ParseIP("fe80::c71:781:4379:abd8"), net.ParseIP("2a01:cb0a:8001:c063:2dd0:198d:c10c:4dbe")})
	info, ok := infoOnInterface(entry, "en0")
	if !ok || info.ID != "0ddebb36" || info.Name != "Salon" || info.Address != "2a01:cb0a:8001:c063:2dd0:198d:c10c:4dbe" {
		t.Fatalf("infoOnInterface = %+v, %v; want the receiver at its global address", info, ok)
	}
}

func TestLinkLocalDiscoveryPreservesInterface(t *testing.T) {
	entry := receiver("tv", castPort, []string{"id=abc"}, nil, []net.IP{net.ParseIP("fe80::123")})
	got, ok := infoOnInterface(entry, "en7")
	if !ok || got.Address != "fe80::123%en7" {
		t.Fatalf("info=%+v,%v", got, ok)
	}
	if _, ok := infoOnInterface(entry, ""); ok {
		t.Fatal("unscoped link-local address accepted")
	}
	entry.AddrIPv4 = []net.IP{net.ParseIP("192.0.2.1")}
	got, ok = infoOnInterface(entry, "en7")
	if !ok || got.Address != "192.0.2.1" {
		t.Fatalf("IPv4 info=%+v,%v", got, ok)
	}
}

func TestAnEntryWithoutItsTXTRecordIsNotADevice(t *testing.T) {
	entry := receiver("Chromecast-abc", castPort, nil, []net.IP{net.ParseIP("192.168.1.5")}, nil)
	if _, ok := infoOnInterface(entry, "en0"); ok {
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
		addresses []string
		want      zeroconf.IPType
	}{
		{[]string{"192.168.1.2"}, zeroconf.IPv4},
		{[]string{"2a01:cb0a::1", "fe80::1"}, zeroconf.IPv6},
		{[]string{"192.0.0.2", "2a01:cb0a::1"}, zeroconf.IPv4AndIPv6},
	} {
		var addresses []netip.Addr
		for _, address := range tt.addresses {
			addresses = append(addresses, netip.MustParseAddr(address))
		}
		if got := trafficOf(addresses); got != tt.want {
			t.Errorf("trafficOf(%v) = %v, want %v", tt.addresses, got, tt.want)
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
