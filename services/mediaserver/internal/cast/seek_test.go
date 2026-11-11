package cast

import (
	"net/url"
	"testing"
	"testing/synctest"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/cast/execute"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/lend"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestASeekMovesTheOriginalFileOnTheDeviceAndPreparesAnyOtherStreamAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		line := lend.NewLine()
		t.Cleanup(line.Leave)
		sought := make(chan time.Duration, 4)
		go func() {
			for {
				var cmd *mediav1.DeviceCommand
				select {
				case cmd = <-line.Outbox():
				case <-line.Done():
					return
				}
				call := cmd.GetCall()
				if seek := call.GetSeek(); seek != nil {
					sought <- seek.GetPosition().AsDuration()
				}
				line.Answer(&mediav1.AnswerRequest{CommandId: call.GetId(), Answer: &mediav1.AnswerRequest_Done_{Done: &mediav1.AnswerRequest_Done{}}})
			}
		}()
		d := lend.NewDevice(line, media.Capabilities{}, func() {}, func(*castorv1.Playhead) {})
		c := &cast{ctx: t.Context(), seeks: make(chan recovery.Seek, 1)}
		c.device.Store(d)
		ctx := t.Context()
		at := &url.URL{Scheme: "http", Host: "castor"}

		_ = d.Play(ctx, execute.Playback{URL: at, Stream: execute.Source{ContentType: media.MP4, Runtime: time.Hour}})
		if err := c.seek(ctx, 20*time.Minute); err != nil {
			t.Fatal(err)
		}
		if got := <-sought; got != 20*time.Minute || len(c.seeks) != 0 {
			t.Errorf("the device sought %v and %d attempts were asked to move, want the device at 20m and none", got, len(c.seeks))
		}

		_ = d.Play(ctx, execute.Playback{URL: at, Stream: execute.Served{ContentType: media.MPEGTS}})
		moved := make(chan time.Duration, 1)
		go func() {
			s := <-c.seeks
			moved <- s.At
			s.Accepted <- nil
		}()
		if err := c.seek(ctx, 5*time.Minute); err != nil {
			t.Fatal(err)
		}
		if got := <-moved; got != 5*time.Minute || len(sought) != 0 {
			t.Errorf("the attempt was moved to %v and the device sought %d times, want the attempt prepared again at 5m", got, len(sought))
		}
	})
}
