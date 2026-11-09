package lend

import (
	"context"
	"errors"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/cast/execute"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/wire"
)

// ErrNotPlaying refuses a pause before the device took a stream.
var ErrNotPlaying = errors.New("the device plays no stream yet")

// Holder is who holds the device paused; it plays while no one does.
type Holder uint8

const (
	Castor Holder = 1 << iota
	Viewer
)

// Device is the lent device as the engine sees it: its lender holds it for the whole cast and plays what it is asked.
type Device struct {
	line  *Line
	caps  media.Capabilities
	left  func()
	heard func(*castorv1.Playhead)

	// mu is held across pause and resume presses, so the viewer's and castor's never interleave.
	mu      sync.Mutex
	holders Holder
	// stream counts the streams handed; what the device says of an earlier one is dropped.
	stream          int
	playing         bool
	begins, runtime time.Duration
	at              time.Duration
	paused          bool
	// askedAt is the last time anyone asked where the device plays, saidAt the last it answered.
	askedAt, saidAt time.Time
}

var _ execute.Device = (*Device)(nil)

// NewDevice plays on the device at the end of line, telling heard where it plays in the whole media.
func NewDevice(line *Line, caps media.Capabilities, left func(), heard func(*castorv1.Playhead)) *Device {
	return &Device{line: line, caps: caps, left: left, heard: heard}
}

// Play points the device at a stream, which plays whatever pause came before.
func (d *Device) Play(ctx context.Context, p execute.Playback) error {
	err := d.done(ctx, &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_Play{Play: played(p)}})
	if errors.Is(err, ErrLenderLeft) {
		// No attempt reaches a device whose lender has left, so recovery tries none.
		d.left()
	}
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.playing, d.holders, d.paused, d.stream = true, 0, false, d.stream+1
	d.begins, d.runtime, d.saidAt = p.Begins, p.Stream.Length(), time.Time{}
	return nil
}

// Reload leaves the stream the device plays behind until the next Play, as when the viewer moved the cast.
func (d *Device) Reload() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.playing, d.stream = false, d.stream+1
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
		play.Stream = &mediav1.DeviceCommand_Play_Playlist_{Playlist: &mediav1.DeviceCommand_Play_Playlist{Duration: runtime(s.Runtime), Segments: wire.Container(s.Segments)}}
	}
	return play
}

func runtime(d time.Duration) *durationpb.Duration {
	if d <= 0 {
		return nil
	}
	return durationpb.New(d)
}

// AwaitEnd waits for the device's end; once its lender has left, the cast's deliveries decide it.
func (d *Device) AwaitEnd(ctx context.Context) error {
	err := d.done(ctx, &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_AwaitEnd{AwaitEnd: &mediav1.DeviceCommand_AwaitEnd{}}})
	if errors.Is(err, ErrLenderLeft) {
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

// Pause and Resume are castor's hold while its read gets ahead.
func (d *Device) Pause(ctx context.Context) error  { return d.Hold(ctx, Castor, true) }
func (d *Device) Resume(ctx context.Context) error { return d.Hold(ctx, Castor, false) }

// Hold sets or ends by's pause, pressing the device when that changes whether it should be paused.
func (d *Device) Hold(ctx context.Context, by Holder, held bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.playing {
		return ErrNotPlaying
	}
	holders := d.holders &^ by
	if held {
		holders |= by
	}
	// Castor only undoes its own hold; the viewer's press also reaches a device paused by its own remote.
	if by == Castor && holders == d.holders {
		return nil
	}
	if pause := holders != 0; pause != (d.holders != 0 || d.paused) {
		press := &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_Resume{Resume: &mediav1.DeviceCommand_Resume{}}}
		if pause {
			press = &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_Pause{Pause: &mediav1.DeviceCommand_Pause{}}}
		}
		if err := d.done(ctx, press); err != nil {
			return err
		}
		d.paused = pause
	}
	d.holders = holders
	d.publish()
	return nil
}

func (d *Device) Position(ctx context.Context) (at time.Duration, paused, ok bool) {
	d.mu.Lock()
	stream := d.stream
	d.askedAt = time.Now()
	d.mu.Unlock()
	a, err := d.line.Call(ctx, &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_Position{Position: &mediav1.DeviceCommand_Position{}}})
	p := a.GetPosition()
	if err != nil || p == nil {
		return 0, false, false
	}
	at, paused = p.GetAt().AsDuration(), p.GetPaused()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stream != stream || !d.playing {
		return 0, false, false
	}
	d.at, d.paused, d.saidAt = at, paused, time.Now()
	d.publish()
	return at, paused, true
}

// Follow asks the device where it plays whenever nothing else has for a while, until ctx ends.
func (d *Device) Follow(ctx context.Context) {
	tick := time.NewTicker(health.HeadEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		d.mu.Lock()
		due := d.playing && time.Since(d.askedAt) >= health.HeadEvery
		d.mu.Unlock()
		if due {
			asking, cancel := context.WithTimeout(ctx, health.HeadEvery)
			d.Position(asking)
			cancel()
		}
	}
}

// publish tells heard where the device plays in the whole media, once it said.
func (d *Device) publish() {
	if !d.playing || d.saidAt.IsZero() {
		return
	}
	head := &castorv1.Playhead{Position: durationpb.New(d.begins + d.at), Paused: d.holders != 0 || d.paused}
	if d.runtime > 0 {
		head.Duration = durationpb.New(d.begins + d.runtime)
	}
	d.heard(head)
}

func (d *Device) Capabilities() media.Capabilities { return d.caps }

func (d *Device) done(ctx context.Context, cmd *mediav1.DeviceCommand_Call) error {
	a, err := d.line.Call(ctx, cmd)
	if err != nil {
		return err
	}
	if e := a.GetError(); e != nil {
		return wire.FromDeviceError(e)
	}
	return nil
}
