package lend

import (
	"context"
	"errors"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/machine"
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
	// deviceRemote is the device's own remote, which holds it while the device says it is paused with no one else holding it.
	deviceRemote
)

// deviceState is what the lent device is doing, as the last stream handed and the last hold left it.
type deviceState uint8

const (
	// noStream is a device that took no stream yet, or was moved off the one it took.
	noStream deviceState = iota
	playing
	// held is a device paused by at least one holder.
	held
)

func (s deviceState) String() string {
	return [...]string{noStream: "no-stream", playing: "playing", held: "held"}[s]
}

type deviceEvent uint8

const (
	streamHanded deviceEvent = iota
	streamLeft
	holdBegun
	holdEnded
)

func (e deviceEvent) String() string {
	return [...]string{streamHanded: "stream-handed", streamLeft: "stream-left", holdBegun: "hold-begun", holdEnded: "hold-ended"}[e]
}

// lentDevice is the lifecycle of a lent device: held exactly while someone holds it, and never without a stream.
func lentDevice() *machine.Machine[deviceState, deviceEvent] {
	return machine.New("lent device", noStream, nil, []machine.Edge[deviceState, deviceEvent]{
		{On: streamHanded, From: []deviceState{noStream, playing, held}, To: playing},
		{On: streamLeft, From: []deviceState{playing, held}, To: noStream},
		{On: holdBegun, From: []deviceState{playing, held}, To: held},
		{On: holdEnded, From: []deviceState{held}, To: playing},
	}, nil)
}

// Device is the lent device as the engine sees it: its lender holds it for the whole cast and plays what it is asked.
type Device struct {
	line  *Line
	caps  media.Capabilities
	left  func()
	heard func(*castorv1.Playhead)

	// mu is held across pause and resume presses, so the viewer's and castor's never interleave.
	mu sync.Mutex
	// moves is held exactly while holders is not empty; only handStream, leaveStream and hold change either.
	moves   *machine.Machine[deviceState, deviceEvent]
	holders Holder
	// changes counts each stream handed and each press; what the device said when asked before one is dropped.
	changes         int
	begins, runtime time.Duration
	at              time.Duration
	// askedAt is the last time anyone asked where the device plays, saidAt the last it answered.
	askedAt, saidAt time.Time
}

var _ execute.Device = (*Device)(nil)

// NewDevice plays on the device at the end of line, telling heard where it plays in the whole media.
func NewDevice(line *Line, caps media.Capabilities, left func(), heard func(*castorv1.Playhead)) *Device {
	return &Device{line: line, caps: caps, left: left, heard: heard, moves: lentDevice()}
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
	d.handStream(ctx)
	d.changes++
	d.begins, d.runtime, d.saidAt = p.Begins, p.Stream.Length(), time.Time{}
	return nil
}

func played(p execute.Playback) *mediav1.DeviceCommand_Play {
	play := &mediav1.DeviceCommand_Play{Url: p.URL.String()}
	for _, c := range p.Captions {
		play.Captions = append(play.Captions, wire.Caption(c, c.URL))
	}
	switch s := p.Stream.(type) {
	case execute.Source:
		source := &mediav1.DeviceCommand_Play_Source{Container: wire.Container(s.ContentType)}
		switch {
		case s.Live:
			source.Length = &mediav1.DeviceCommand_Play_Source_Live{Live: &mediav1.DeviceCommand_Play_Live{}}
		case s.Runtime > 0:
			source.Length = &mediav1.DeviceCommand_Play_Source_Duration{Duration: durationpb.New(s.Runtime)}
		}
		play.Stream = &mediav1.DeviceCommand_Play_Source_{Source: source}
	case execute.Served:
		served := &mediav1.DeviceCommand_Play_Progressive{Container: wire.Container(s.ContentType)}
		switch {
		case s.Live:
			served.Timeline = &mediav1.DeviceCommand_Play_Progressive_Live{Live: &mediav1.DeviceCommand_Play_Live{}}
		case s.Runtime != nil:
			runtime := &mediav1.DeviceCommand_Play_Runtime{Duration: durationpb.New(s.Runtime.Length), Seek: mediav1.DeviceCommand_Play_Runtime_SEEK_START}
			if s.Runtime.TimeSeek {
				runtime.Seek = mediav1.DeviceCommand_Play_Runtime_SEEK_TIME
			}
			served.Timeline = &mediav1.DeviceCommand_Play_Progressive_Runtime{Runtime: runtime}
		}
		play.Stream = &mediav1.DeviceCommand_Play_Progressive_{Progressive: served}
	case execute.Playlist:
		playlist := &mediav1.DeviceCommand_Play_Playlist{Segments: wire.Container(s.Segments)}
		switch {
		case s.Live:
			playlist.Length = &mediav1.DeviceCommand_Play_Playlist_Live{Live: &mediav1.DeviceCommand_Play_Live{}}
		case s.Runtime > 0:
			playlist.Length = &mediav1.DeviceCommand_Play_Playlist_Duration{Duration: durationpb.New(s.Runtime)}
		}
		if s.EmbeddedCaptions {
			playlist.EmbeddedCaptions = &mediav1.DeviceCommand_Play_EmbeddedCaptions{}
		}
		play.Stream = &mediav1.DeviceCommand_Play_Playlist_{Playlist: playlist}
	}
	return play
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

// Reload leaves the stream the device plays behind until the next Play, as when the viewer moved the cast.
func (d *Device) Reload() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.leaveStream()
	d.changes++
}

// handStream is a stream handed, which plays whatever pause came before.
func (d *Device) handStream(ctx context.Context) {
	d.holders = 0
	d.moves.Take(ctx, streamHanded)
}

// leaveStream is the device moved off its stream, which no one holds any longer.
func (d *Device) leaveStream() {
	d.holders = 0
	d.moves.Take(context.Background(), streamLeft)
}

// hold is who holds the device now, paused while anyone does.
func (d *Device) hold(ctx context.Context, holders Holder) {
	d.holders = holders
	if holders != 0 {
		d.moves.Take(ctx, holdBegun)
		return
	}
	d.moves.Take(ctx, holdEnded)
}

func (d *Device) streaming() bool { return !d.moves.Is(noStream) }

// Pause and Resume are castor's hold while its read gets ahead.
func (d *Device) Pause(ctx context.Context) error  { return d.Hold(ctx, Castor, true) }
func (d *Device) Resume(ctx context.Context) error { return d.Hold(ctx, Castor, false) }

// Hold sets or ends by's pause, pressing the device when that changes whether it should be paused.
func (d *Device) Hold(ctx context.Context, by Holder, held bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.streaming() {
		return ErrNotPlaying
	}
	holders := d.holders &^ by
	if held {
		holders |= by
	}
	// The viewer's resume ends their pause wherever they pressed it.
	if by == Viewer && !held {
		holders &^= deviceRemote
	}
	if pause := holders != 0; pause != (d.holders != 0) {
		if err := d.done(ctx, press(pause)); err != nil {
			return err
		}
		d.changes++
	}
	d.hold(ctx, holders)
	d.publish()
	return nil
}

// press is the call that pauses the device, or resumes it.
func press(pause bool) *mediav1.DeviceCommand_Call {
	if pause {
		return &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_Pause{Pause: &mediav1.DeviceCommand_Pause{}}}
	}
	return &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_Resume{Resume: &mediav1.DeviceCommand_Resume{}}}
}

func (d *Device) Position(ctx context.Context) (at time.Duration, paused, ok bool) {
	d.mu.Lock()
	changes := d.changes
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
	if d.changes != changes || !d.streaming() {
		return 0, false, false
	}
	d.at, d.saidAt = at, time.Now()
	switch {
	case !paused && d.holders&deviceRemote != 0:
		// The viewer played on from the device's own remote, ending their pause; castor's, which no press made, is pressed now.
		holders := d.holders &^ (deviceRemote | Viewer)
		if holders != 0 && d.done(ctx, press(true)) == nil {
			d.changes++
			paused = true
		} else {
			holders = 0
		}
		d.hold(ctx, holders)
	case d.holders == 0 && paused:
		d.hold(ctx, deviceRemote)
	}
	d.publish()
	return at, paused, true
}

// Follow asks the device where it plays whenever nothing else has for a while, until ctx ends.
func (d *Device) Follow(ctx context.Context) {
	timer := time.NewTimer(health.HeadEvery)
	defer timer.Stop()
	defer d.moves.End(context.WithoutCancel(ctx))
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		wait := health.HeadEvery
		d.mu.Lock()
		if d.streaming() {
			wait = time.Until(d.askedAt.Add(health.HeadEvery))
		}
		d.mu.Unlock()
		if wait > 0 {
			timer.Reset(wait)
			continue
		}
		timer.Reset(health.HeadEvery)

		asking, cancel := context.WithTimeout(ctx, health.HeadEvery)
		d.Position(asking)
		cancel()
	}
}

// publish tells heard where the device plays in the whole media, once it said.
func (d *Device) publish() {
	if !d.streaming() || d.saidAt.IsZero() {
		return
	}
	head := &castorv1.Playhead{Position: durationpb.New(d.begins + d.at), Paused: d.moves.Is(held)}
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
