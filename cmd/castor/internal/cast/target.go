package cast

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/gen/castor/v1/castorv1connect"
	"github.com/stupside/castor/internal/settings"
)

// Device is the device the commands cast to: pinned at its host, or found by id or by name.
type Device struct {
	Name string `yaml:"name" validate:"required_without_all=Host ID"`
	Type string `yaml:"type" validate:"required"`
	Host string `yaml:"host"`
	ID   string `yaml:"id"`
}

// Is reports whether found is the device d names: by id when it sets one, else by name.
func (d Device) Is(found *castorv1.Device) bool {
	if d.ID != "" {
		return found.GetId() == d.ID
	}
	return strings.EqualFold(found.GetName(), d.Name)
}

// Target is the device d names: pinned at its host, or found by id or name among those the API lists.
func Target(ctx context.Context, devices castorv1connect.DeviceServiceClient, d Device) (*castorv1.Target, error) {
	kind, err := configuredDeviceType(d.Type)
	if err != nil {
		return nil, err
	}
	if d.Host != "" {
		return &castorv1.Target{Target: &castorv1.Target_Pinned_{Pinned: &castorv1.Target_Pinned{Type: kind, Address: d.Host}}}, nil
	}
	listed, err := devices.ListDevices(ctx, &castorv1.ListDevicesRequest{})
	if err != nil {
		return nil, err
	}
	matches := slices.DeleteFunc(slices.Clone(listed.GetDevices()), func(found *castorv1.Device) bool {
		return found.GetType() != kind || !d.Is(found)
	})
	label := cmp.Or(d.ID, d.Name)
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("device %q (type %s) not found", label, d.Type)
	case 1:
		return &castorv1.Target{Target: &castorv1.Target_DeviceId{DeviceId: matches[0].GetId()}}, nil
	default:
		return nil, fmt.Errorf("%d devices of type %s are named %q: set device.id (castor scan lists ids) or device.host", len(matches), d.Type, label)
	}
}

func configuredDeviceType(name string) (castorv1.DeviceType, error) {
	kind, ok := settings.ParseEnum[castorv1.DeviceType](name)
	if !ok {
		return kind, fmt.Errorf("unknown device type: %q", name)
	}
	return kind, nil
}

// DeviceTypeName is the family name shown by scan and the device picker, also used in configuration.
func DeviceTypeName(kind castorv1.DeviceType) string {
	return cmp.Or(settings.EnumName(kind), "unknown")
}
