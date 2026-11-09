package lend

import (
	"errors"
	"net/url"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/cast/execute"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// remote is a device that says it plays at, paused when its own remote paused it, and records every press it takes.
type remote struct {
	presses []string
	plays   []*mediav1.DeviceCommand_Play
	heads   []*castorv1.Playhead
	paused  bool
}

func (r *remote) device(t *testing.T, at time.Duration) *Device {
	t.Helper()
	l, sent := attached(t)
	t.Cleanup(l.Leave)
	go func() {
		for {
			var cmd *mediav1.DeviceCommand
			select {
			case cmd = <-sent:
			case <-l.Done():
				return
			}
			call := cmd.GetCall()
			answer := &mediav1.AnswerRequest{CommandId: call.GetId(), Answer: &mediav1.AnswerRequest_Done_{Done: &mediav1.AnswerRequest_Done{}}}
			switch op := call.GetOperation().(type) {
			case *mediav1.DeviceCommand_Call_Play:
				r.presses = append(r.presses, "play")
				r.plays = append(r.plays, op.Play)
			case *mediav1.DeviceCommand_Call_Pause:
				r.presses = append(r.presses, "pause")
			case *mediav1.DeviceCommand_Call_Resume:
				r.presses = append(r.presses, "resume")
			case *mediav1.DeviceCommand_Call_Position:
				answer.Answer = &mediav1.AnswerRequest_Position_{Position: &mediav1.AnswerRequest_Position{At: durationpb.New(at), Paused: r.paused}}
			}
			l.Answer(answer)
		}
	}()
	return NewDevice(l, media.Capabilities{}, func() {}, func(head *castorv1.Playhead) { r.heads = append(r.heads, head) })
}

// fromAMinuteIn is ninety minutes of the media handed from a minute in.
var fromAMinuteIn = execute.Playback{URL: &url.URL{Scheme: "http", Host: "castor"}, Stream: execute.Source{Runtime: 90 * time.Minute}, Begins: time.Minute}

func TestTheDevicePlaysOnlyOnceNeitherTheViewerNorCastorHoldsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &remote{}
		d := r.device(t, 0)
		ctx := t.Context()
		for _, err := range []error{
			d.Play(ctx, fromAMinuteIn),
			d.Hold(ctx, Viewer, true),
			d.Resume(ctx),
			d.Pause(ctx),
			d.Hold(ctx, Viewer, false),
			d.Hold(ctx, Viewer, false),
			d.Resume(ctx),
		} {
			if err != nil {
				t.Fatal(err)
			}
		}
		if want := []string{"play", "pause", "resume"}; !slices.Equal(r.presses, want) {
			t.Errorf("the device was pressed %v, want %v", r.presses, want)
		}
	})
}

func TestANewStreamPlaysWhateverPauseCameBefore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &remote{}
		d := r.device(t, 0)
		ctx := t.Context()
		if err := d.Hold(ctx, Viewer, true); !errors.Is(err, ErrNotPlaying) {
			t.Errorf("holding a device handed nothing yet: %v, want %v", err, ErrNotPlaying)
		}
		_ = d.Play(ctx, fromAMinuteIn)
		_ = d.Hold(ctx, Viewer, true)
		_ = d.Play(ctx, fromAMinuteIn)
		_ = d.Hold(ctx, Viewer, true)
		if want := []string{"play", "pause", "play", "pause"}; !slices.Equal(r.presses, want) {
			t.Errorf("the device was pressed %v, want %v", r.presses, want)
		}
	})
}

func TestThePlayheadIsInTheWholeMediaNotTheStreamHanded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &remote{}
		d := r.device(t, 30*time.Second)
		ctx := t.Context()
		d.Position(ctx)
		_ = d.Play(ctx, fromAMinuteIn)
		_ = d.Hold(ctx, Viewer, true)
		d.Position(ctx)
		if len(r.heads) != 1 {
			t.Fatalf("heard %v, want one playhead: none before the device took a stream or said where it plays", r.heads)
		}
		if head := r.heads[0]; head.GetPosition().AsDuration() != 90*time.Second || head.GetDuration().AsDuration() != 91*time.Minute || !head.GetPaused() {
			t.Errorf("playhead %v, want 1m30s into 1h31m, paused by the viewer", head)
		}
	})
}

func TestAPlaylistTellsTheDeviceWhatItIsCutInto(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &remote{}
		d := r.device(t, 0)
		if err := d.Play(t.Context(), execute.Playback{URL: &url.URL{Scheme: "http", Host: "castor"}, Stream: execute.Playlist{Segments: media.MP4}}); err != nil {
			t.Fatal(err)
		}
		if got := r.plays[0].GetPlaylist().GetSegments(); got != mediav1.Container_CONTAINER_MP4 {
			t.Errorf("segments = %v, want MP4", got)
		}
	})
}

func TestTheViewerResumesADeviceItsOwnRemotePaused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &remote{paused: true}
		d := r.device(t, 0)
		ctx := t.Context()
		_ = d.Play(ctx, fromAMinuteIn)
		d.Position(ctx)
		if err := d.Hold(ctx, Viewer, false); err != nil {
			t.Fatal(err)
		}
		_ = d.Hold(ctx, Viewer, true)
		_ = d.Hold(ctx, Viewer, true)
		if want := []string{"play", "resume", "pause"}; !slices.Equal(r.presses, want) {
			t.Errorf("the device was pressed %v, want %v: resumed though no holder paused it, and paused once however often asked", r.presses, want)
		}
		if head := r.heads[len(r.heads)-1]; !head.GetPaused() {
			t.Errorf("playhead %v, want paused", head)
		}
		r.paused = false
		_ = d.Hold(ctx, Viewer, false)
		if head := r.heads[len(r.heads)-1]; head.GetPaused() {
			t.Errorf("playhead %v after the viewer resumed, want playing", head)
		}
	})
}

func TestWhatTheDeviceSaysOfAStreamItWasMovedOffIsDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &remote{}
		d := r.device(t, 30*time.Second)
		ctx := t.Context()
		_ = d.Play(ctx, fromAMinuteIn)
		d.Reload()
		if _, _, ok := d.Position(ctx); ok || len(r.heads) != 0 {
			t.Errorf("a position of the stream the viewer moved off was kept: %v", r.heads)
		}
		if err := d.Hold(ctx, Viewer, true); !errors.Is(err, ErrNotPlaying) {
			t.Errorf("holding a device between streams: %v, want %v", err, ErrNotPlaying)
		}
	})
}

func TestCastorNeverResumesADeviceItsViewerPaused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &remote{paused: true}
		d := r.device(t, 0)
		ctx := t.Context()
		_ = d.Play(ctx, fromAMinuteIn)
		d.Position(ctx)
		_ = d.Resume(ctx)
		if want := []string{"play"}; !slices.Equal(r.presses, want) {
			t.Errorf("the device was pressed %v, want %v: castor held nothing, so it resumes nothing", r.presses, want)
		}
	})
}
