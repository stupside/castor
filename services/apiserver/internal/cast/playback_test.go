package cast

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

type playbackDevice struct {
	device.Device
	head                    device.Playhead
	controls                device.Controls
	positionErr, commandErr error
	pause                   func(context.Context) error
	commands                int
}

func (d *playbackDevice) Position(context.Context) (device.Playhead, error) {
	return d.head, d.positionErr
}
func (d *playbackDevice) Controls() device.Controls { return d.controls }
func (d *playbackDevice) Pause(ctx context.Context) error {
	d.commands++
	if d.pause != nil {
		return d.pause(ctx)
	}
	if d.commandErr != nil {
		return d.commandErr
	}
	d.head.Paused = true
	return nil
}
func (d *playbackDevice) Resume(context.Context) error {
	d.commands++
	d.head.Paused = false
	return d.commandErr
}
func (d *playbackDevice) Seek(_ context.Context, at time.Duration) error {
	d.commands++
	d.head.At = at
	return d.commandErr
}

func playbackFixture(t *testing.T) (*Service, *cast, *playbackDevice, context.CancelCauseFunc) {
	t.Helper()
	s := New(nil, nil, nil, nil)
	t.Cleanup(func() { s.Drain(t.Context()) })
	lifetime, stop := context.WithCancelCause(s.ctx)
	c := newCast("playing", &castorv1.Device{}, &castorv1.CastRequest{}, stop)
	d := &playbackDevice{head: device.Playhead{At: 30 * time.Second}, controls: device.Controls{Pause: true, Seek: true}}
	p := &playback{Device: d, lifetime: lifetime}
	p.loaded.Store(&loadedPlayback{duration: 2 * time.Minute})
	c.playback.Store(p)
	done := make(chan struct{})
	close(done)
	if err := s.casts.Add(c.id, c, done); err != nil {
		t.Fatal(err)
	}
	return s, c, d, stop
}

func TestPlaybackControlsUseTheExistingCastAndKeepPauseDuringSeek(t *testing.T) {
	s, c, d, _ := playbackFixture(t)
	paused, err := s.Control(t.Context(), &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Pause{Pause: &emptypb.Empty{}}})
	if err != nil || !paused.GetPlayback().GetPaused() {
		t.Fatalf("pause=%v, %v", paused, err)
	}
	seeked, err := s.Control(t.Context(), &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Seek{Seek: durationpb.New(40 * time.Second)}})
	if err != nil || seeked.GetPlayback().GetPosition().AsDuration() != 40*time.Second || !seeked.GetPlayback().GetPaused() {
		t.Fatalf("paused seek=%v, %v", seeked, err)
	}
	resumed, err := s.Control(t.Context(), &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Resume{Resume: &emptypb.Empty{}}})
	if err != nil || resumed.GetPlayback().GetPaused() || d.commands != 3 {
		t.Fatalf("resume=%v, %v; commands=%d", resumed, err, d.commands)
	}
	view, _ := c.now.Load()
	if view.ended != nil {
		t.Fatal("a playback control ended the cast")
	}
}

func TestPlaybackDoesNotInventAnUnknownPosition(t *testing.T) {
	s, c, d, _ := playbackFixture(t)
	d.positionErr = device.ErrNoPosition
	response, err := s.Playback(t.Context(), &castorv1.PlaybackRequest{CastId: c.id})
	if err != nil || response.GetPlayback().GetPosition() != nil || response.GetPlayback().GetCanSeek() {
		t.Fatalf("unknown playhead=%v, %v", response, err)
	}
	_, err = s.Control(t.Context(), &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Pause{Pause: &emptypb.Empty{}}})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || d.commands != 0 {
		t.Fatalf("unready control=%v, commands=%d", err, d.commands)
	}
}

func TestInvalidOrUnsupportedSeekDoesNotReachTheDevice(t *testing.T) {
	s, c, d, _ := playbackFixture(t)
	for _, at := range []time.Duration{-time.Second, 3 * time.Minute} {
		_, err := s.Control(t.Context(), &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Seek{Seek: durationpb.New(at)}})
		if connect.CodeOf(err) != connect.CodeOutOfRange {
			t.Fatalf("seek %s: %v", at, err)
		}
	}
	d.controls.Seek = false
	_, err := s.Control(t.Context(), &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Seek{Seek: durationpb.New(time.Second)}})
	if connect.CodeOf(err) != connect.CodeUnimplemented || d.commands != 0 {
		t.Fatalf("unsupported seek=%v, commands=%d", err, d.commands)
	}
}

func TestPlaybackCommandFailureDoesNotStopOrForgetTheCast(t *testing.T) {
	s, c, d, _ := playbackFixture(t)
	d.commandErr = errors.New("receiver temporarily unavailable")
	_, err := s.Control(t.Context(), &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Pause{Pause: &emptypb.Empty{}}})
	view, _ := c.now.Load()
	if err == nil || view.ended != nil || c.playback.Load().lifetime.Err() != nil {
		t.Fatalf("control failure=%v ended=%v", err, view.ended)
	}
}

func TestAnInFlightPlaybackCommandEndsWithItsCast(t *testing.T) {
	s, c, d, stop := playbackFixture(t)
	started := make(chan struct{})
	d.pause = func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() }
	result := make(chan error, 1)
	go func() {
		_, err := s.Control(t.Context(), &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Pause{Pause: &emptypb.Empty{}}})
		result <- err
	}()
	<-started
	stop(context.Canceled)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("command outlived cast: %v", err)
	}
	_, err := s.Control(t.Context(), &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Pause{Pause: &emptypb.Empty{}}})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || d.commands != 1 {
		t.Fatalf("ended cast accepted command: %v, commands=%d", err, d.commands)
	}
}
