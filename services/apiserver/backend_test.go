package apiserver

import (
	"slices"
	"testing"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/transport"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

func TestEveryDeviceTypeTheContractNamesHasAFamily(t *testing.T) {
	cfg := defaults()
	families := cfg.backend(transport.Endpoint{}, transport.Endpoint{}).Devices.Families
	for number, name := range castorv1.DeviceType_name {
		kind := castorv1.DeviceType(number)
		if kind == castorv1.DeviceType_DEVICE_TYPE_UNSPECIFIED {
			continue
		}
		if !slices.ContainsFunc(families, func(f device.Family) bool {
			return device.Info{ID: "tv", Type: f.Type()}.Public().GetType() == kind
		}) {
			t.Errorf("%s has no device family, so a cast pinned to it can never connect", name)
		}
	}
}
