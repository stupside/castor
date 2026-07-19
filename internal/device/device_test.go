package device

import (
	"net"
	"testing"

	castdns "github.com/vishen/go-chromecast/dns"
)

func TestChromecastInfo(t *testing.T) {
	entry := castdns.CastEntry{
		DeviceName: "Office Display",
		AddrV4:     net.ParseIP("192.0.2.10"),
	}

	got, ok := chromecastInfo(entry)
	if !ok {
		t.Fatal("chromecastInfo() rejected a complete entry")
	}
	want := Info{Name: "Office Display", Type: TypeChromecast, Address: "192.0.2.10"}
	if got != want {
		t.Errorf("chromecastInfo() = %+v, want %+v", got, want)
	}
}

func TestChromecastInfoRejectsIncompleteEntry(t *testing.T) {
	if _, ok := chromecastInfo(castdns.CastEntry{DeviceName: "Office Display"}); ok {
		t.Error("chromecastInfo() accepted an entry without an IPv4 address")
	}
}
