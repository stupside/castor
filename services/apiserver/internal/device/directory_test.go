package device

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/connect"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

func directoryWithFamilies(reach Registry) *Directory {
	return NewDirectory(reach, NetworkDiscovery(reach.Families))
}

// echoing is a family whose devices answer every sweep more than once, as SSDP and mDNS announce.
type echoing struct{ stub }

func (echoing) Discover(context.Context) []Info {
	bedroom := Info{ID: "uuid-1", Name: "Bedroom", Type: "push", Address: "10.0.0.9"}
	kitchen := Info{ID: "uuid-2", Name: "Kitchen", Type: "push", Address: "10.0.0.8"}
	return []Info{bedroom, kitchen, bedroom}
}

func TestADeviceAnnouncedTwiceIsListedOnce(t *testing.T) {
	d := directoryWithFamilies(Registry{Families: []Family{echoing{}}, Timeout: time.Second})
	listed, err := d.ListDevices(t.Context(), &castorv1.ListDevicesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found := listed.GetDevices()
	if len(found) != 2 || found[0].GetId() != "push:uuid-1" || found[1].GetId() != "push:uuid-2" {
		t.Errorf("ListDevices = %+v, want Bedroom then Kitchen, once each", found)
	}
}

// moving is a family whose one device answers only at its current address, which changes as a DHCP lease would.
type moving struct {
	stub
	at        string
	connected atomic.Pointer[Info]
}

type typedStub struct {
	stub
	kind Type
}

func (s typedStub) Type() Type { return s.kind }

func TestPinnedDeviceWithDisabledFamilyIsRejected(t *testing.T) {
	d := directoryWithFamilies(Registry{Families: []Family{typedStub{kind: "dlna"}}})
	got, err := d.Target(t.Context(), &castorv1.Target{Target: &castorv1.Target_Pinned_{Pinned: &castorv1.Target_Pinned{Type: castorv1.DeviceType_DEVICE_TYPE_ROKU, Address: "10.0.0.9"}}})
	if connect.CodeOf(err) != connect.CodeInvalidArgument || got != (Info{}) {
		t.Errorf("Target = %+v, %v, want no device and invalid_argument", got, err)
	}
}

func (m *moving) Discover(context.Context) []Info {
	return []Info{{ID: "uuid-1", Name: "Bedroom", Type: "push", Address: m.at}}
}

func (m *moving) Connect(_ context.Context, info Info) (Device, error) {
	m.connected.Store(&info)
	if info.Address != m.at {
		return nil, errors.New("no answer")
	}
	return idle{}, nil
}

type idle struct{}

func (idle) Play(context.Context, Playback) error {
	return nil
}
func (idle) Position(context.Context) (Playhead, error) { return Playhead{}, ErrNoPosition }

func (idle) AwaitEnd(ctx context.Context) error  { return context.Cause(ctx) }
func (idle) Capabilities() *mediav1.Capabilities { return &mediav1.Capabilities{} }
func (idle) Close() error                        { return nil }

func TestADeviceThatMovedSinceItWasListedIsFoundAgainToConnect(t *testing.T) {
	family := &moving{at: "10.0.0.9"}
	d := directoryWithFamilies(Registry{Families: []Family{family}, Timeout: time.Second})
	listed, err := d.ListDevices(t.Context(), &castorv1.ListDevicesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	family.at = "10.0.0.7"
	target, err := d.Target(t.Context(), &castorv1.Target{Target: &castorv1.Target_DeviceId{DeviceId: listed.GetDevices()[0].GetId()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Connect(t.Context(), target); err != nil {
		t.Fatalf("Connect = %v, want the device reached at its new address", err)
	}
	if got := family.connected.Load(); got.Address != "10.0.0.7" {
		t.Errorf("family connected %+v, want the device at its new address", got)
	}
}

func (idle) Stop(context.Context) error   { return nil }
func (idle) Pause(context.Context) error  { return nil }
func (idle) Resume(context.Context) error { return nil }

type manyDevices struct{ stub }

func (manyDevices) Discover(context.Context) []Info {
	var found []Info
	for i := range maxSeenDevices + 10 {
		found = append(found, Info{ID: fmt.Sprint(i), Type: "push", Address: fmt.Sprint(i)})
	}
	return found
}
func TestDiscoveryCacheExpiresWithoutReleasingActiveOwnership(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := directoryWithFamilies(Registry{Families: []Family{manyDevices{}}, Timeout: time.Second})
		d.discover(t.Context())
		if len(d.seen) != maxSeenDevices {
			t.Fatalf("cache grew to %d", len(d.seen))
		}
		target := Info{Type: "push", ID: "owned", Address: "192.0.2.1"}
		l := newLease(d)
		if err := l.claimKeys(target, []string{endpoint(target)}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(seenDeviceTTL)
		_, _ = d.lookup("missing")
		if len(d.seen) != 0 {
			t.Fatal("discovery cache did not expire")
		}
		if err := newLease(d).claimKeys(target, []string{endpoint(target)}); err == nil {
			t.Fatal("cache expiration released active lease")
		}
		l.release()
	})
}
