package dlna

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/huin/goupnp"
)

func TestFindServiceReachesAVersion3OnlyDevice(t *testing.T) {
	service := func(serviceType string) goupnp.Service {
		return goupnp.Service{
			ServiceType: serviceType,
			ControlURL:  goupnp.URLField{URL: url.URL{Scheme: "http", Host: "192.0.2.10:2870", Path: "/control"}, Ok: true},
		}
	}
	const v3 = "urn:schemas-upnp-org:service:AVTransport:3"
	loc := &url.URL{Scheme: "http", Host: "192.0.2.10:2870", Path: "/dmr.xml"}

	// Philips 50PUD6654/43 and similar devices publish the v3 service only.
	root := &goupnp.RootDevice{Device: goupnp.Device{Services: []goupnp.Service{service(v3)}}}
	got, err := findService(root, loc, "AVTransport")
	if err != nil || got.Service.ServiceType != v3 {
		t.Fatalf("findService() = %q, %v, want %q", got.Service.ServiceType, err, v3)
	}

	bare := &goupnp.RootDevice{Device: goupnp.Device{Services: []goupnp.Service{service("urn:schemas-upnp-org:service:RenderingControl:3")}}}
	if _, err := findService(bare, loc, "AVTransport"); err == nil {
		t.Error("a device without any AVTransport was accepted")
	}
}

func TestATypedPortIsNotWhereSSDPIsAsked(t *testing.T) {
	for typed, want := range map[string]string{
		"192.0.2.10":      "192.0.2.10:1900",
		"192.0.2.10:1534": "192.0.2.10:1900",
		"tv.local:9197":   "tv.local:1900",
		"fe80::1":         "[fe80::1]:1900",
		"[fe80::1]:1534":  "[fe80::1]:1900",
		"fe80::1%en0":     "[fe80::1%en0]:1900",
	} {
		if got := ssdpTarget(typed); got != want {
			t.Errorf("ssdpTarget(%q) = %q, want %q", typed, got, want)
		}
	}
}

func TestCancellingSSDPSearchInterruptsTheRead(t *testing.T) {
	peer, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	result := make(chan error, 1)
	go func() {
		_, err := searchDescription(ctx, peer.LocalAddr().String())
		result <- err
	}()
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := peer.ReadFrom(make([]byte, 2048)); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("cast stopped")
	cancel(cause)
	select {
	case err := <-result:
		if !errors.Is(err, cause) {
			t.Fatalf("searchDescription = %v, want cancellation cause", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled SSDP search kept waiting for the device")
	}
}

func TestSSDPSearchWithoutCallerDeadlineStillEnds(t *testing.T) {
	peer, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	started := time.Now()
	_, err = searchDescription(context.Background(), peer.LocalAddr().String())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unanswered SSDP search = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > searchTimeout+time.Second {
		t.Fatalf("unanswered SSDP search waited %s beyond its %s budget", elapsed, searchTimeout)
	}
}
