package settings

import (
	"testing"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

func TestAnEnumIsSpelledInConfigurationAsTheContractNamesIt(t *testing.T) {
	if got := EnumName(castorv1.DeviceType_DEVICE_TYPE_CHROMECAST); got != "chromecast" {
		t.Errorf("EnumName = %q, want chromecast", got)
	}
	if got := EnumName(castorv1.DeviceType_DEVICE_TYPE_UNSPECIFIED); got != "" {
		t.Errorf("the unspecified value is named %q, want none", got)
	}
	if got, ok := ParseEnum[castorv1.Delivery]("SERVE"); !ok || got != castorv1.Delivery_DELIVERY_SERVE {
		t.Errorf("ParseEnum(SERVE) = %v, %v; want DELIVERY_SERVE", got, ok)
	}
	for _, name := range []string{"unspecified", "", "laser"} {
		if _, ok := ParseEnum[castorv1.DeviceType](name); ok {
			t.Errorf("ParseEnum(%q) took it, want refused", name)
		}
	}
}
