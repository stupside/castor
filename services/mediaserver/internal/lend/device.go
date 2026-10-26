package lend

import (
	"context"
	"errors"
	"net/url"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/mediaserver/internal/cast/execute"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/wire"
)

// Device is the lent device as the engine sees it: its lender holds it for the whole cast and plays what it is asked.
type Device struct {
	line *Line
	caps media.Capabilities
	// left ends the cast once its lender has gone.
	left func()
}

var _ execute.Device = Device{}

func NewDevice(line *Line, caps media.Capabilities, left func()) Device {
	return Device{line: line, caps: caps, left: left}
}

func (d Device) Play(ctx context.Context, streamURL *url.URL, contentType string, captions []media.Caption) error {
	play := &mediav1.DeviceCommand_Play{Url: streamURL.String(), Container: wire.Container(contentType)}
	for _, c := range captions {
		play.Captions = append(play.Captions, wire.Caption(c, c.URL))
	}
	err := d.done(ctx, &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_Play{Play: play}})
	if errors.Is(err, ErrLenderLeft) {
		// No attempt reaches a device whose lender has left, so recovery tries none.
		d.left()
	}
	return err
}

// AwaitEnd waits for the device's end; once its lender has left, the cast's deliveries decide it.
func (d Device) AwaitEnd(ctx context.Context) error {
	err := d.done(ctx, &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_AwaitEnd{AwaitEnd: &mediav1.DeviceCommand_AwaitEnd{}}})
	if errors.Is(err, ErrLenderLeft) {
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

func (d Device) Pause(ctx context.Context) error {
	return d.done(ctx, &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_Pause{Pause: &mediav1.DeviceCommand_Pause{}}})
}

func (d Device) Resume(ctx context.Context) error {
	return d.done(ctx, &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_Resume{Resume: &mediav1.DeviceCommand_Resume{}}})
}

func (d Device) Position(ctx context.Context) (at time.Duration, paused, ok bool) {
	a, err := d.line.Call(ctx, &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_Position{Position: &mediav1.DeviceCommand_Position{}}})
	p := a.GetPosition()
	if err != nil || p == nil {
		return 0, false, false
	}
	return p.GetAt().AsDuration(), p.GetPaused(), true
}

func (d Device) Capabilities() media.Capabilities { return d.caps }

func (d Device) done(ctx context.Context, cmd *mediav1.DeviceCommand_Call) error {
	a, err := d.line.Call(ctx, cmd)
	if err != nil {
		return err
	}
	if e := a.GetError(); e != nil {
		return wire.FromDeviceError(e)
	}
	return nil
}
