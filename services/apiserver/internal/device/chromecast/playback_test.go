package chromecast

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"

	castmedia "github.com/vishen/go-chromecast/cast"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/device/devicetest"
)

func TestChromecastPlaybackState(t *testing.T) {
	const content = "https://castor.test/stream.mp4"
	status := func(session int, mediaID, playerState, idleReason string) castmedia.MediaStatusResponse {
		return castmedia.MediaStatusResponse{
			Type: "MEDIA_STATUS",
			Status: []castmedia.Media{{
				MediaSessionId: session,
				PlayerState:    playerState,
				IdleReason:     idleReason,
				Media:          castmedia.MediaItem{ContentId: mediaID},
			}},
		}
	}
	closed := castmedia.MediaStatusResponse{Type: "CLOSE"}
	for _, tt := range []struct {
		name     string
		messages []castmedia.MediaStatusResponse
		want     bool
		wantErr  bool
	}{
		{"stale finished status is ignored", []castmedia.MediaStatusResponse{status(4, "https://old.test/movie.mp4", "IDLE", "FINISHED")}, false, false},
		{"finish after playing ends", []castmedia.MediaStatusResponse{status(7, content, "PLAYING", ""), status(7, content, "IDLE", "FINISHED")}, true, false},
		{"another session cannot end this one", []castmedia.MediaStatusResponse{status(7, content, "PLAYING", ""), status(8, "https://other.test/movie.mp4", "IDLE", "FINISHED")}, false, false},
		{"pause remains active", []castmedia.MediaStatusResponse{status(7, content, "PLAYING", ""), status(7, content, "PAUSED", "")}, false, false},
		{"receiver close after playing ends", []castmedia.MediaStatusResponse{status(7, content, "PLAYING", ""), closed}, true, false},
		{"receiver close while still loading does not end", []castmedia.MediaStatusResponse{closed}, false, false},
		{"receiver playback error fails", []castmedia.MediaStatusResponse{status(7, content, "PLAYING", ""), status(7, content, "IDLE", "ERROR")}, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var watch playback
			watch.begin(content)
			got := false
			var gotErr error
			for i := range tt.messages {
				got, gotErr = playbackOutcome(&watch, &tt.messages[i])
			}
			if got != tt.want {
				t.Errorf("playbackOutcome = %v, want %v", got, tt.want)
			}
			if (gotErr != nil) != tt.wantErr {
				t.Errorf("playbackOutcome error = %v, want error %v", gotErr, tt.wantErr)
			}
		})
	}
}

func TestChromecastAnswersWhenTheCastEnds(t *testing.T) {
	devicetest.AwaitsTheCastsEnd(t, func() device.Device {
		return &session{ch: &channel{gone: make(chan struct{})}, ending: newEnding()}
	})
}

func TestAChromecastThatDropsTheConnectionIsGone(t *testing.T) {
	near, far := net.Pipe()
	dev := &session{name: "Living Room", ending: newEnding()}
	dev.ch = newChannel(near, dev.watchMessage)
	t.Cleanup(func() { _ = dev.Close() })
	_ = far.Close()
	if _, gone := errors.AsType[*device.Gone](dev.AwaitEnd(t.Context())); !gone {
		t.Error("a receiver that went away mid-cast is not reported gone, so the cast outlives it")
	}
}

func TestChromecastReceiverErrorCodeReachesTheCastError(t *testing.T) {
	t.Run("code is read from an ERROR message", func(t *testing.T) {
		code, ok := receiverError([]byte(`{"type":"ERROR","requestId":0,"detailedErrorCode":100,"severity":2,"itemId":1}`))
		if !ok || code != 100 {
			t.Errorf("receiverError = (%d, %v), want (100, true)", code, ok)
		}
	})
	t.Run("other messages carry no code", func(t *testing.T) {
		for _, payload := range []string{`{"type":"MEDIA_STATUS","status":[]}`, `{"type":"ERROR"}`, `not json`} {
			if _, ok := receiverError([]byte(payload)); ok {
				t.Errorf("receiverError(%s) reports a code", payload)
			}
		}
	})

	const content = "https://castor.test/stream.mp4"
	status := func(session int, mediaID, playerState, idleReason string) castmedia.MediaStatusResponse {
		return castmedia.MediaStatusResponse{
			Type: "MEDIA_STATUS",
			Status: []castmedia.Media{{
				MediaSessionId: session,
				PlayerState:    playerState,
				IdleReason:     idleReason,
				Media:          castmedia.MediaItem{ContentId: mediaID},
			}},
		}
	}
	playEnds := func(watch *playback) (bool, error) {
		messages := []castmedia.MediaStatusResponse{
			status(7, content, "PLAYING", ""),
			status(7, content, "IDLE", "ERROR"),
		}
		var over bool
		var err error
		for i := range messages {
			over, err = playbackOutcome(watch, &messages[i])
		}
		return over, err
	}
	t.Run("the code is named when playback ends in error", func(t *testing.T) {
		var watch playback
		watch.begin(content)
		watch.errorCode = 100
		over, err := playEnds(&watch)
		if !over || err == nil {
			t.Fatalf("playbackOutcome = (%v, %v), want the playback to end in error", over, err)
		}
		for _, want := range []string{"chromecast playback ended with receiver error", "100"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err.Error(), want)
			}
		}
	})
	t.Run("the text is unchanged without a code", func(t *testing.T) {
		var watch playback
		watch.begin(content)
		over, err := playEnds(&watch)
		if !over || err == nil {
			t.Fatalf("playbackOutcome = (%v, %v), want the playback to end in error", over, err)
		}
		if got, want := err.Error(), "chromecast playback ended with receiver error"; got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
	})
	t.Run("a new playback forgets the previous code", func(t *testing.T) {
		var watch playback
		watch.begin(content)
		watch.errorCode = 100
		watch.begin(content)
		if watch.errorCode != 0 {
			t.Errorf("errorCode = %d after a new playback began, want 0", watch.errorCode)
		}
	})
	t.Run("the receiver's own messages end the cast with its code", func(t *testing.T) {
		dev := &session{ch: &channel{gone: make(chan struct{})}, ending: newEnding()}
		dev.watch.begin(content)
		for _, payload := range []string{
			`{"type":"MEDIA_STATUS","status":[{"mediaSessionId":7,"playerState":"PLAYING","media":{"contentId":"` + content + `"}}]}`,
			`{"type":"ERROR","requestId":0,"detailedErrorCode":100,"severity":2,"itemId":1}`,
			`{"type":"MEDIA_STATUS","status":[{"mediaSessionId":7,"playerState":"IDLE","idleReason":"ERROR","media":{"contentId":"` + content + `"}}]}`,
		} {
			dev.watchMessage([]byte(payload))
		}
		err := dev.AwaitEnd(t.Context())
		if err == nil || !strings.Contains(err.Error(), "chromecast playback ended with receiver error") || !strings.Contains(err.Error(), "100") {
			t.Errorf("the cast ended with %v, want the receiver error naming code 100", err)
		}
	})
}

// A receiver that refuses the LOAD itself still names its code in the error Play returns.
func TestChromecastLoadRefusalKeepsTheReceiverErrorCode(t *testing.T) {
	for _, verdict := range []string{"idle with an error", "LOAD_FAILED"} {
		t.Run(verdict, func(t *testing.T) {
			stream, err := url.Parse("https://castor.test/stream.mp4")
			if err != nil {
				t.Fatal(err)
			}
			dev := receiverAnswering(t, func(id int, load mediaItem) []any {
				notice := jsontext.Value(`{"type":"ERROR","requestId":0,"detailedErrorCode":100,"severity":2,"itemId":1}`)
				var answer any = mediaStatus(id, load.ContentId, "IDLE", "ERROR")
				if verdict == "LOAD_FAILED" {
					answer = jsontext.Value(fmt.Sprintf(`{"type":"LOAD_FAILED","requestId":%d}`, id))
				}
				return []any{notice, answer}
			})
			err = dev.Play(t.Context(), device.Playback{URL: stream, Play: devicetest.Source(mediav1.Container_CONTAINER_MP4)})
			if err == nil || !strings.Contains(err.Error(), "receiver detailed error code 100") {
				t.Fatalf("Play() = %v, want the refusal to name the receiver's detailed error code 100", err)
			}
		})
	}
	t.Run("a refusal without a code keeps its text", func(t *testing.T) {
		stream, err := url.Parse("https://castor.test/stream.mp4")
		if err != nil {
			t.Fatal(err)
		}
		dev := receiverAnswering(t, func(id int, load mediaItem) []any {
			return []any{mediaStatus(id, load.ContentId, "IDLE", "ERROR")}
		})
		err = dev.Play(t.Context(), device.Playback{URL: stream, Play: devicetest.Source(mediav1.Container_CONTAINER_MP4)})
		if err == nil || strings.Contains(err.Error(), "detailed error code") {
			t.Fatalf("Play() = %v, want a refusal that names no code", err)
		}
	})
}
