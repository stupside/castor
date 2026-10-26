package lend

import (
	"context"
	"errors"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

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

func (d Device) Play(ctx context.Context, p execute.Playback) error {
	err := d.done(ctx, &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_Play{Play: played(p)}})
	if errors.Is(err, ErrLenderLeft) {
		// No attempt reaches a device whose lender has left, so recovery tries none.
		d.left()
	}
	return err
}

func played(p execute.Playback) *mediav1.DeviceCommand_Play {
	play := &mediav1.DeviceCommand_Play{Url: p.URL.String()}
	for _, c := range p.Captions {
		play.Captions = append(play.Captions, wire.Caption(c, c.URL))
	}
	switch s := p.Stream.(type) {
	case execute.Source:
		play.Stream = &mediav1.DeviceCommand_Play_Source_{Source: &mediav1.DeviceCommand_Play_Source{Container: wire.Container(s.ContentType), Duration: runtime(s.Runtime)}}
	case execute.Served:
		served := &mediav1.DeviceCommand_Play_Progressive{Container: wire.Container(s.ContentType)}
		if r := s.Runtime; r != nil {
			served.Runtime = &mediav1.DeviceCommand_Play_Runtime{Duration: durationpb.New(r.Length), Seek: mediav1.DeviceCommand_Play_Runtime_SEEK_START}
			if r.TimeSeek {
				served.Runtime.Seek = mediav1.DeviceCommand_Play_Runtime_SEEK_TIME
			}
		}
		play.Stream = &mediav1.DeviceCommand_Play_Progressive_{Progressive: served}
	case execute.Playlist:
		play.Stream = &mediav1.DeviceCommand_Play_Playlist_{Playlist: &mediav1.DeviceCommand_Play_Playlist{Duration: runtime(s.Runtime)}}
	}
	return play
}

// runtime is unset where the source declared none.
func runtime(d time.Duration) *durationpb.Duration {
	if d <= 0 {
		return nil
	}
	return durationpb.New(d)
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
