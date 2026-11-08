package cast

import (
	"context"
	"net/url"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/device/devicetest"
)

// stillDevice plays anything and says it is at head.
type stillDevice struct {
	device.Device
	head time.Duration
}

func (stillDevice) Play(context.Context, device.Playback) error { return nil }
func (d stillDevice) Position(context.Context) (device.Playhead, error) {
	return device.Playhead{At: d.head}, nil
}

func TestARevisedStreamIsPlacedWhereItBeginsInTheCast(t *testing.T) {
	accepted := make(chan struct{})
	close(accepted)
	r := &playbackRun{playback: &playback{Device: stillDevice{head: 5 * time.Second}, lifetime: t.Context()},
		offered: make(chan struct{}), accept: accepted, ready: make(chan error, 1)}
	u, _ := url.Parse("http://192.0.2.1/stream.ts")
	for _, tt := range []struct {
		name          string
		start         *durationpb.Duration
		runtime       time.Duration
		position, all time.Duration
	}{
		{"the first stream", durationpb.New(0), 119 * time.Second, 5 * time.Second, 119 * time.Second},
		{"a revision resuming 15 s in", durationpb.New(15 * time.Second), 104 * time.Second, 20 * time.Second, 119 * time.Second},
		{"a media server that does not say", nil, 119 * time.Second, 5 * time.Second, 119 * time.Second},
	} {
		play := devicetest.Served(mediav1.Container_CONTAINER_MPEGTS, tt.runtime, mediav1.DeviceCommand_Play_Runtime_SEEK_TIME)
		play.Start = tt.start
		if err := r.Play(t.Context(), device.Playback{URL: u, Play: play}); err != nil {
			t.Fatal(err)
		}
		state, err := r.state(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if got := state.GetPosition().AsDuration(); got != tt.position {
			t.Errorf("%s: position %v, want %v", tt.name, got, tt.position)
		}
		if got := state.GetDuration().AsDuration(); got != tt.all {
			t.Errorf("%s: duration %v, want %v", tt.name, got, tt.all)
		}
	}
}
