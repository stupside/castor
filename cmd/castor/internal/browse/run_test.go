package browse

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/stupside/castor/cmd/castor/internal/cast"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// A nil device client proves the pinned device is neither listed nor picked.
func TestAPinnedDeviceIsCastToWithoutPicking(t *testing.T) {
	to, name, typ, err := pickDevice(t.Context(), nil, cast.Device{Type: "dlna", Host: "http://10.0.0.9:9197/dmr"})
	if err != nil {
		t.Fatal(err)
	}
	want := &castorv1.Target{Target: &castorv1.Target_Pinned_{Pinned: &castorv1.Target_Pinned{Type: castorv1.DeviceType_DEVICE_TYPE_DLNA, Address: "http://10.0.0.9:9197/dmr"}}}
	if !proto.Equal(to, want) {
		t.Errorf("targeted %v, want the pinned %v", to, want)
	}
	if name != "http://10.0.0.9:9197/dmr" || typ != "dlna" {
		t.Errorf("badged %q %q, want the pinned device's host and type", name, typ)
	}
}
