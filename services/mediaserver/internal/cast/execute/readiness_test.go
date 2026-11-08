package execute

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// resumedDevice keeps the playback it was handed.
type resumedDevice struct {
	fakeDevice
	handed Playback
}

func (d *resumedDevice) Play(_ context.Context, p Playback) error {
	d.handed = p
	return nil
}

func TestAResumedStreamTellsTheDeviceWhereItBegins(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:1/stream.ts")
	ready, err := certifyPresentation(presentation{target: u, contentType: media.MPEGTS, runtime: 104 * time.Second,
		readiness: outputValidated, delivery: &delivery{}, reached: ran{began: 15 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	dev := &resumedDevice{}
	if err := ready.play(t.Context(), dev); err != nil {
		t.Fatal(err)
	}
	if dev.handed.Start != 15*time.Second {
		t.Fatalf("the device was told the stream begins at %v, want 15s, where the attempt resumed", dev.handed.Start)
	}
}
