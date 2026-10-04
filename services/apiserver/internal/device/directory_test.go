package device

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"connectrpc.com/connect"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// moving is a family whose one device answers only at its current address, which changes as a DHCP lease would.
type moving struct {
	stub
	at string
}

type typedStub struct {
	stub
	kind Type
}

func (s typedStub) Type() Type { return s.kind }

func TestPinnedDeviceWithDisabledFamilyIsRejected(t *testing.T) {
	d := NewDirectory(Registry{Families: []Family{typedStub{kind: "dlna"}}})
	got, err := d.Target(t.Context(), &castorv1.Target{Target: &castorv1.Target_Pinned_{Pinned: &castorv1.Target_Pinned{Type: castorv1.DeviceType_DEVICE_TYPE_ROKU, Address: "10.0.0.9"}}})
	if connect.CodeOf(err) != connect.CodeInvalidArgument || got != (Info{}) {
		t.Errorf("Target = %+v, %v, want no device and invalid_argument", got, err)
	}
}

func (m *moving) Discover(context.Context) []Info {
	return []Info{{ID: "uuid-1", Name: "Bedroom", Type: "push", Address: m.at}}
}

func (m *moving) Connect(_ context.Context, info Info) (Device, error) {
	if info.Address != m.at {
		return nil, errors.New("no answer")
	}
	return idle{}, nil
}

type idle struct{}

func (idle) Play(context.Context, *url.URL, mediav1.Container) error { return nil }
func (idle) AwaitEnd(ctx context.Context) error                      { return context.Cause(ctx) }
func (idle) Capabilities() *mediav1.Capabilities                     { return &mediav1.Capabilities{} }
func (idle) Close() error                                            { return nil }

func TestADeviceThatMovedSinceItWasListedIsFoundAgainToConnect(t *testing.T) {
	family := &moving{at: "10.0.0.9"}
	d := NewDirectory(Registry{Families: []Family{family}, Timeout: time.Second})
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
		t.Errorf("Connect = %v, want the device reached at its new address", err)
	}
}
