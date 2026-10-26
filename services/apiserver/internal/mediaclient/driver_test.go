package mediaclient

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

type waitingDevice struct{ started chan struct{} }

func (*waitingDevice) Play(context.Context, device.Playback) error {
	return nil
}
func (*waitingDevice) Capabilities() *mediav1.Capabilities {
	return &mediav1.Capabilities{Formats: []*mediav1.FormatSupport{{Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MP4}}, Tracks: &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_H264{H264: &mediav1.H264Support{}}}, Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_AAC}}}}}}
}
func (*waitingDevice) Position(context.Context) (device.Playhead, error) {
	return device.Playhead{}, device.ErrNoPosition
}

func (d *waitingDevice) AwaitEnd(ctx context.Context) error {
	d.started <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}

func TestDuplicateCommandIDsDoNotLoseCancellationOfTheFirstCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, leave := context.WithCancelCause(t.Context())
		defer leave(nil)
		d := &driver{ctx: ctx, leave: leave, device: &waitingDevice{started: make(chan struct{}, 2)}, running: map[string]context.CancelFunc{}}
		cmd := &mediav1.DeviceCommand{Command: &mediav1.DeviceCommand_Call_{Call: &mediav1.DeviceCommand_Call{Id: "await-1", Operation: &mediav1.DeviceCommand_Call_AwaitEnd{AwaitEnd: &mediav1.DeviceCommand_AwaitEnd{}}}}}
		d.run(cmd)
		synctest.Wait()
		d.run(cmd)
		synctest.Wait()
		released := make(chan struct{})
		go func() { d.release(); close(released) }()
		select {
		case <-released:
		case <-time.After(time.Second):
			leave(nil)
			<-released
			t.Fatal("duplicate IDs lost the first call's cancellation and wedged device release")
		}
	})
}

func TestDriveRejectsTooManyOutstandingDeviceCalls(t *testing.T) {
	ctx, leave := context.WithCancelCause(t.Context())
	defer leave(nil)
	d := &driver{ctx: ctx, leave: leave, device: &waitingDevice{started: make(chan struct{}, maxDeviceCalls)}, running: map[string]context.CancelFunc{}}
	for i := range maxDeviceCalls + 1 {
		cmd := &mediav1.DeviceCommand{Command: &mediav1.DeviceCommand_Call_{Call: &mediav1.DeviceCommand_Call{Id: fmt.Sprintf("await-%d", i), Operation: &mediav1.DeviceCommand_Call_AwaitEnd{AwaitEnd: &mediav1.DeviceCommand_AwaitEnd{}}}}}
		err := d.run(cmd)
		if i < maxDeviceCalls && err != nil {
			t.Fatalf("command %d refused below the limit: %v", i, err)
		}
		if i == maxDeviceCalls && (err == nil || !strings.Contains(err.Error(), "concurrent device commands")) {
			t.Fatalf("command %d = %v, want the drive to reject excessive calls", i, err)
		}
	}
	d.release()
}

func (*waitingDevice) Stop(context.Context) error   { return nil }
func (*waitingDevice) Close() error                 { return nil }
func (*waitingDevice) Pause(context.Context) error  { return nil }
func (*waitingDevice) Resume(context.Context) error { return nil }
