package cast

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

type listedDevices []*castorv1.Device

func (devices listedDevices) ListDevices(context.Context, *castorv1.ListDevicesRequest) (*castorv1.ListDevicesResponse, error) {
	return &castorv1.ListDevicesResponse{Devices: devices}, nil
}

func (listedDevices) Describe(context.Context, *castorv1.DescribeRequest) (*castorv1.DescribeResponse, error) {
	return &castorv1.DescribeResponse{}, nil
}

func (listedDevices) WatchDevices(context.Context, *castorv1.WatchDevicesRequest) (*connect.ServerStreamForClient[castorv1.WatchDevicesResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not watched here"))
}

func (listedDevices) StartPairing(context.Context, *castorv1.StartPairingRequest) (*castorv1.StartPairingResponse, error) {
	return &castorv1.StartPairingResponse{}, nil
}

func (listedDevices) FinishPairing(context.Context, *castorv1.FinishPairingRequest) (*castorv1.FinishPairingResponse, error) {
	return &castorv1.FinishPairingResponse{}, nil
}

func (listedDevices) CancelPairing(context.Context, *castorv1.CancelPairingRequest) (*castorv1.CancelPairingResponse, error) {
	return &castorv1.CancelPairingResponse{}, nil
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

func TestEveryDeviceTypeTheContractNamesCanBeConfigured(t *testing.T) {
	for number, name := range castorv1.DeviceType_name {
		kind := castorv1.DeviceType(number)
		if kind == castorv1.DeviceType_DEVICE_TYPE_UNSPECIFIED {
			continue
		}
		if got, err := configuredDeviceType(DeviceTypeName(kind)); err != nil || got != kind {
			t.Errorf("%s has no device.type name, so scan lists it as unknown and no config can pin it", name)
		}
	}
}
