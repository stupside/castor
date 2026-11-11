package cast

import (
	"context"
	"strings"
	"testing"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

type listedDevices []*castorv1.Device

func (devices listedDevices) ListDevices(context.Context, *castorv1.ListDevicesRequest) (*castorv1.ListDevicesResponse, error) {
	return &castorv1.ListDevicesResponse{Devices: devices}, nil
}

func TestUnknownConfiguredDeviceTypesFailBeforeListingOrPinning(t *testing.T) {
	for _, host := range []string{"", "10.0.0.9"} {
		got, err := Target(t.Context(), nil, Device{Name: "Bedroom", Type: "other", Host: host})
		if got != nil || err == nil || !strings.Contains(err.Error(), "unknown device type") {
			t.Errorf("Target with host %q = %v, %v, want unknown device type", host, got, err)
		}
	}
}

func TestDiscoveredDeviceIsMatchedByEnumAndName(t *testing.T) {
	devices := listedDevices{
		{Id: "roku:wrong", Name: "Bedroom", Type: castorv1.DeviceType_DEVICE_TYPE_ROKU},
		{Id: "dlna:right", Name: "BEDROOM", Type: castorv1.DeviceType_DEVICE_TYPE_DLNA},
	}
	got, err := Target(t.Context(), devices, Device{Name: "bedroom", Type: "dlna"})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetDeviceId() != "dlna:right" {
		t.Errorf("Target = %v, want dlna:right", got)
	}
}

func TestDeviceIDBeatsName(t *testing.T) {
	devices := listedDevices{
		{Id: "dlna:a", Name: "TV", Type: castorv1.DeviceType_DEVICE_TYPE_DLNA},
		{Id: "dlna:b", Name: "TV", Type: castorv1.DeviceType_DEVICE_TYPE_DLNA},
	}
	got, err := Target(t.Context(), devices, Device{Name: "TV", ID: "dlna:b", Type: "dlna"})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetDeviceId() != "dlna:b" {
		t.Errorf("Target = %v, want dlna:b", got)
	}
}

func TestSharedNameIsAmbiguousNotFirstWins(t *testing.T) {
	devices := listedDevices{
		{Id: "dlna:a", Name: "TV", Type: castorv1.DeviceType_DEVICE_TYPE_DLNA},
		{Id: "dlna:b", Name: "TV", Type: castorv1.DeviceType_DEVICE_TYPE_DLNA},
	}
	got, err := Target(t.Context(), devices, Device{Name: "TV", Type: "dlna"})
	if got != nil || err == nil || !strings.Contains(err.Error(), "device.id") {
		t.Errorf("Target = %v, %v, want an error naming device.id", got, err)
	}
}

// The picker preselects and a cast targets by the same rule, so a TV listed by two families is one device of the type configured.
func TestADeviceIsNamedOnlyWithinItsType(t *testing.T) {
	tv := Device{Name: "TV", Type: "chromecast"}
	for found, want := range map[*castorv1.Device]bool{
		{Id: "chromecast:1", Name: "TV", Type: castorv1.DeviceType_DEVICE_TYPE_CHROMECAST}: true,
		{Id: "dlna:1", Name: "TV", Type: castorv1.DeviceType_DEVICE_TYPE_DLNA}:             false,
	} {
		if got := tv.Is(found); got != want {
			t.Errorf("Is(%s) = %t, want %t", found.GetId(), got, want)
		}
	}
	if (Device{}).Is(&castorv1.Device{Id: "dlna:2", Type: castorv1.DeviceType_DEVICE_TYPE_DLNA}) {
		t.Error("no device configured named a device without a name")
	}
}
