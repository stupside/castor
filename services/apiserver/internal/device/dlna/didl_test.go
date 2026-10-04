package dlna

import (
	"net/url"
	"strings"
	"testing"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
)

func TestADeviceIsHandedEachContainerUnderItsDLNAProfile(t *testing.T) {
	stream := &url.URL{Scheme: "http", Host: "192.0.2.1:8080", Path: "/stream"}
	for container, want := range map[mediav1.Container]string{
		mediav1.Container_CONTAINER_MPEGTS: "http-get:*:video/mp2t:DLNA.ORG_PN=MPEG_TS_HD_NA_ISO;DLNA.ORG_OP=00;DLNA.ORG_CI=1;DLNA.ORG_FLAGS=8D300000000000000000000000000000",
		mediav1.Container_CONTAINER_MP4:    "http-get:*:video/mp4:DLNA.ORG_PN=AVC_MP4_HP_HD_AAC;DLNA.ORG_OP=00;DLNA.ORG_CI=1;DLNA.ORG_FLAGS=01300000000000000000000000000000",
	} {
		metadata, err := buildDIDLMetadata(stream, container)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(metadata, `protocolInfo="`+want+`"`) {
			t.Errorf("%v was announced as %s, want protocolInfo %q", container, metadata, want)
		}
	}
}
