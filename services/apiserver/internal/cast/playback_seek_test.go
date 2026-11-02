package cast

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/device/devicetest"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

type sourcePlayer struct {
	device.Device
	mu           sync.Mutex
	head         device.Playhead
	plays, seeks int
	fail         bool
	resetSeek    bool
	seekPaused   bool
	end          chan struct{}
}

func (d *sourcePlayer) Play(context.Context, device.Playback) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail {
		d.fail = false
		return errors.New("receiver refused new LOAD")
	}
	d.plays++
	d.head = device.Playhead{Seekable: &device.SeekWindow{End: 30 * time.Second}}
	d.end = make(chan struct{})
	return nil
}
func (d *sourcePlayer) Position(context.Context) (device.Playhead, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.head, nil
}
func (d *sourcePlayer) Controls() device.Controls { return device.Controls{Pause: true, Seek: true} }
func (d *sourcePlayer) Seek(_ context.Context, at time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seeks++
	d.seekPaused = d.head.Paused
	if d.resetSeek {
		d.head.At = 0
		return errors.New("receiver acknowledged seek but returned to the portion's start")
	}
	if d.head.Seekable != nil {
		at = max(d.head.Seekable.Start, min(at, d.head.Seekable.End))
	}
	d.head.At = at
	return nil
}

func TestSeekFreezesTheOldPictureBeforePreparingTheRequestedPosition(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s, c, d, active := sourceSeekFixture(t, func(ctx context.Context, at time.Duration) error {
		if at == 0 {
			return nil
		}
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := s.Control(t.Context(), &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Seek{Seek: durationpb.New(time.Hour)}})
		done <- err
	}()
	<-entered
	d.mu.Lock()
	plays := d.plays
	paused := d.head.Paused
	d.mu.Unlock()
	if plays != 1 || active.Load() != 2 || c.playback.Load().loaded.Load().base != 0 {
		t.Fatal("old picture was replaced before the target was prepared")
	}
	if !paused {
		t.Error("the old scene kept playing during source preparation")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if active.Load() != 1 || c.playback.Load().loaded.Load().base != time.Hour {
		t.Fatal("handoff did not join the old pipeline")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.head.Paused {
		t.Fatal("replacement did not resume the originally playing video")
	}
}

func TestAReceiverSeekThatResetsIsHeldUntilSourcePreparationReplacesIt(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s, c, d, _ := sourceSeekFixture(t, func(ctx context.Context, at time.Duration) error {
		if at == 0 {
			return nil
		}
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	d.mu.Lock()
	d.head.At, d.resetSeek = 10*time.Second, true
	d.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := seekSource(t, s, c, 20*time.Second); done <- err }()
	<-entered
	d.mu.Lock()
	paused, plays, seeks := d.head.Paused, d.plays, d.seeks
	d.mu.Unlock()
	if !paused || plays != 1 || seeks != 1 {
		t.Errorf("receiver reset is visible before replacement: paused=%v loads=%d seeks=%d", paused, plays, seeks)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	r, err := s.Playback(t.Context(), &castorv1.PlaybackRequest{CastId: c.id})
	if err != nil || r.GetPlayback().GetPosition().AsDuration() != 20*time.Second || r.GetPlayback().GetPaused() {
		t.Fatalf("replacement position=%v, %v", r, err)
	}
}
func (d *sourcePlayer) Pause(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.head.Paused = true
	return nil
}
func (d *sourcePlayer) Resume(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.head.Paused = false
	return nil
}
func (d *sourcePlayer) AwaitEnd(ctx context.Context) error {
	d.mu.Lock()
	end := d.end
	d.mu.Unlock()
	select {
	case <-end:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func sourceSeekFixture(t *testing.T, prepare func(context.Context, time.Duration) error) (*Service, *cast, *sourcePlayer, *atomic.Int32) {
	t.Helper()
	s, c, _, stop := playbackFixture(t)
	ctx := c.playback.Load().lifetime
	d := &sourcePlayer{}
	p := &playback{Device: d, lifetime: ctx, reload: true, requests: make(chan *playbackSeek)}
	c.playback.Store(p)
	started := make(chan time.Duration, 8)
	done := make(chan playbackResult, 1)
	active := &atomic.Int32{}
	run := func(ctx context.Context, id string, at time.Duration, player device.Device, show func(*castorv1.CastStatus)) (*castorv1.Ended, error) {
		active.Add(1)
		defer active.Add(-1)
		if prepare != nil {
			if err := prepare(ctx, at); err != nil {
				return nil, err
			}
		}
		play := devicetest.Playlist()
		play.GetPlaylist().Duration = durationpb.New(2*time.Hour - at)
		u, _ := url.Parse("http://fixture.test/" + id + "/master.m3u8")
		if err := player.Play(ctx, device.Playback{URL: u, Play: play}); err != nil {
			return nil, err
		}
		started <- at
		<-ctx.Done()
		return &castorv1.Ended{Result: &castorv1.Ended_Stopped{Stopped: &emptypb.Empty{}}}, nil
	}
	go func() { ended, err := p.drive(ctx, c.id, 0, c.show, run); done <- playbackResult{ended, err} }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("initial source did not start")
	}
	t.Cleanup(func() {
		stop(context.Canceled)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("source workers outlived their cast")
		}
		if active.Load() != 0 {
			t.Error("source worker leaked")
		}
	})
	return s, c, d, active
}

func seekSource(t *testing.T, s *Service, c *cast, at time.Duration) (*castorv1.ControlResponse, error) {
	t.Helper()
	return s.Control(t.Context(), &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Seek{Seek: durationpb.New(at)}})
}

func TestFarSeekPreparesSourceAtTheRequestedPositionAndKeepsGlobalTime(t *testing.T) {
	s, c, d, active := sourceSeekFixture(t, nil)
	for _, at := range []time.Duration{54 * time.Minute, 53*time.Minute + 59*time.Second} {
		r, err := seekSource(t, s, c, at)
		if err != nil || r.GetPlayback().GetPosition().AsDuration() != at || r.GetPlayback().GetDuration().AsDuration() != 2*time.Hour {
			t.Fatalf("global seek %s = %v, %v", at, r, err)
		}
		if active.Load() != 1 {
			t.Fatalf("old source not joined: %d", active.Load())
		}
	}
	r, err := seekSource(t, s, c, 54*time.Minute+9*time.Second)
	if err != nil || r.GetPlayback().GetPosition().AsDuration() != 54*time.Minute+9*time.Second {
		t.Fatalf("local seek=%v, %v", r, err)
	}
	if _, err := s.Control(t.Context(), &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Pause{Pause: &emptypb.Empty{}}}); err != nil {
		t.Fatal(err)
	}
	r, err = seekSource(t, s, c, 56*time.Minute)
	if err != nil || !r.GetPlayback().GetPaused() {
		t.Fatalf("paused source reposition=%v, %v", r, err)
	}
	d.mu.Lock()
	plays, seeks := d.plays, d.seeks
	d.mu.Unlock()
	if plays != 4 || seeks != 1 {
		t.Fatalf("pipeline loads=%d, local seeks=%d", plays, seeks)
	}
	view, _ := c.now.Load()
	if view.ended != nil || c.id != "playing" {
		t.Fatal("seek replaced the user's cast")
	}
}

func TestLocalSeekHoldsThePictureAndRestoresItsPreviousPauseState(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{true: "paused", false: "playing"}[paused], func(t *testing.T) {
			s, c, d, _ := sourceSeekFixture(t, nil)
			d.mu.Lock()
			d.head.Paused = paused
			d.mu.Unlock()
			r, err := seekSource(t, s, c, 20*time.Second)
			if err != nil || r.GetPlayback().GetPosition().AsDuration() != 20*time.Second || r.GetPlayback().GetPaused() != paused {
				t.Fatalf("local seek=%v, %v", r, err)
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			if !d.seekPaused || d.plays != 1 || d.seeks != 1 {
				t.Fatalf("local seek was not held: paused=%v plays=%d seeks=%d", d.seekPaused, d.plays, d.seeks)
			}
		})
	}
}

func TestFailedOrCancelledPreparationKeepsTheOriginalCastUsable(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{true: "cancelled", false: "source failed"}[cancelled], func(t *testing.T) {
			s, c, d, active := sourceSeekFixture(t, func(ctx context.Context, at time.Duration) error {
				if at == 0 {
					return nil
				}
				if cancelled {
					<-ctx.Done()
					return ctx.Err()
				}
				return errors.New("source request failed")
			})
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			_, err := s.Control(ctx, &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Seek{Seek: durationpb.New(time.Hour)}})
			if err == nil {
				t.Fatal("failed preparation accepted")
			}
			if active.Load() != 1 {
				t.Fatalf("wrong number of surviving pipelines: %d", active.Load())
			}
			d.mu.Lock()
			paused := d.head.Paused
			d.mu.Unlock()
			if paused {
				t.Fatal("failed or cancelled preparation left the original playback frozen")
			}
			r, err := s.Control(t.Context(), &castorv1.ControlRequest{CastId: c.id, Action: &castorv1.ControlRequest_Pause{Pause: &emptypb.Empty{}}})
			if err != nil || !r.GetPlayback().GetPaused() {
				t.Fatalf("control after failure=%v, %v", r, err)
			}
			d.mu.Lock()
			plays := d.plays
			d.mu.Unlock()
			if plays != 1 {
				t.Fatal("failed preparation replaced the original video")
			}
		})
	}
}

func TestARefusedReplacementLoadRestoresThePreviousPublication(t *testing.T) {
	s, c, d, active := sourceSeekFixture(t, nil)
	previous := c.playback.Load().loaded.Load()
	d.mu.Lock()
	d.head.At = 20 * time.Second
	d.head.Paused = true
	d.fail = true
	d.mu.Unlock()
	if _, err := seekSource(t, s, c, time.Hour); err == nil {
		t.Fatal("refused LOAD accepted")
	}
	if c.playback.Load().loaded.Load() != previous || active.Load() != 1 {
		t.Fatal("original cast was not restored")
	}
	r, err := s.Playback(t.Context(), &castorv1.PlaybackRequest{CastId: c.id})
	if err != nil || r.GetPlayback().GetPosition().AsDuration() != 20*time.Second || !r.GetPlayback().GetPaused() {
		t.Fatalf("restored state=%v, %v", r, err)
	}
}
