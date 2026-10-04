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
