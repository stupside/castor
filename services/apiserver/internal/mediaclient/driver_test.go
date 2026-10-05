package mediaclient

import (
	"context"
	"net/url"
	"testing"
	"testing/synctest"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
)

type waitingDevice struct{ started chan struct{} }

func (*waitingDevice) Play(context.Context, *url.URL, mediav1.Container) error { return nil }
func (*waitingDevice) Capabilities() *mediav1.Capabilities                     { return &mediav1.Capabilities{} }
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
		cmd := &mediav1.DeviceCommand{Id: "await-1", Command: &mediav1.DeviceCommand_AwaitEnd_{AwaitEnd: &mediav1.DeviceCommand_AwaitEnd{}}}
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
