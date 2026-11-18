package chromecast

import (
	"errors"
	"net"
	"testing"

	castmedia "github.com/vishen/go-chromecast/cast"

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
		started  bool
		over     bool
		wantErr  error
	}{
		{"stale finished status is ignored", []castmedia.MediaStatusResponse{status(4, "https://old.test/movie.mp4", "IDLE", "FINISHED")}, false, false, nil},
		{"a stale status does not take the Play's session from the media it loaded", []castmedia.MediaStatusResponse{status(4, "https://old.test/movie.mp4", "IDLE", "FINISHED"), status(7, content, "PLAYING", "")}, true, false, nil},
		{"the media reported is not yet playing", []castmedia.MediaStatusResponse{status(7, content, "IDLE", "")}, false, false, nil},
		{"finish after playing ends", []castmedia.MediaStatusResponse{status(7, content, "PLAYING", ""), status(7, content, "IDLE", "FINISHED")}, true, true, nil},
		{"finish before playing does not end", []castmedia.MediaStatusResponse{status(7, content, "IDLE", ""), status(7, content, "IDLE", "FINISHED")}, false, false, nil},
		{"another session cannot end this one", []castmedia.MediaStatusResponse{status(7, content, "PLAYING", ""), status(8, "https://other.test/movie.mp4", "IDLE", "FINISHED")}, true, false, nil},
		{"pause remains active", []castmedia.MediaStatusResponse{status(7, content, "PLAYING", ""), status(7, content, "PAUSED", "")}, true, false, nil},
		{"receiver close after playing ends", []castmedia.MediaStatusResponse{status(7, content, "PLAYING", ""), closed}, true, true, nil},
		{"receiver close while still loading does not end", []castmedia.MediaStatusResponse{closed}, false, false, nil},
		{"receiver close before playing does not end", []castmedia.MediaStatusResponse{status(7, content, "IDLE", ""), closed}, false, false, nil},
		{"receiver playback error fails", []castmedia.MediaStatusResponse{status(7, content, "PLAYING", ""), status(7, content, "IDLE", "ERROR")}, true, true, errFailed},
		{"receiver error before playing refuses the media", []castmedia.MediaStatusResponse{status(7, content, "IDLE", ""), status(7, content, "IDLE", "ERROR")}, false, true, errRefused},
		{"nothing follows an end", []castmedia.MediaStatusResponse{status(7, content, "PLAYING", ""), status(7, content, "IDLE", "FINISHED"), status(7, content, "PLAYING", ""), status(7, content, "IDLE", "ERROR")}, true, true, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlayback(t.Context(), content, "transport-1", false)
			for i := range tt.messages {
				p.apply(&tt.messages[i])
			}
			if p.life.Started() != tt.started {
				t.Errorf("the device has played = %v, want %v", p.life.Started(), tt.started)
			}
			over := false
			select {
			case <-p.ending.Finished():
				over = true
			default:
			}
			if over != tt.over {
				t.Errorf("the Play's end is signalled = %v, want %v", over, tt.over)
			}
			if over && !errors.Is(p.ending.Err(), tt.wantErr) {
				t.Errorf("the Play ended on %v, want %v", p.ending.Err(), tt.wantErr)
			}
		})
	}
}

func TestChromecastAnswersWhenTheCastEnds(t *testing.T) {
	devicetest.AwaitsTheCastsEnd(t, func() device.Device {
		return &session{ch: &channel{gone: make(chan struct{})}}
	})
}

func TestAChromecastThatDropsTheConnectionIsGone(t *testing.T) {
	near, far := net.Pipe()
	dev := &session{name: "Living Room"}
	dev.ch = newChannel(near, dev.watchMessage)
	t.Cleanup(func() { _ = dev.Close() })
	_ = far.Close()
	if _, gone := errors.AsType[*device.Gone](dev.AwaitEnd(t.Context())); !gone {
		t.Error("a receiver that went away mid-cast is not reported gone, so the cast outlives it")
	}
}

func TestTheTracksAStatusReportsAreThoseOfTheAskedMediaSession(t *testing.T) {
	payload := []byte(`{"type":"MEDIA_STATUS","status":[
		{"mediaSessionId":6,"media":{"tracks":[{"trackId":1,"type":"VIDEO"}]}},
		{"mediaSessionId":7,"media":{"tracks":[{"trackId":1,"type":"VIDEO"},{"trackId":3,"type":"TEXT","language":"en","name":"Transcription"}]}}]}`)
	tracks := reportedTracks(payload, 7)
	if len(tracks) != 2 || tracks[1].Type != "TEXT" || tracks[1].Language != "en" || tracks[1].Name != "Transcription" {
		t.Fatalf("reportedTracks = %+v", tracks)
	}
	if tracks := reportedTracks(payload, 8); tracks != nil {
		t.Fatalf("tracks of a session the status does not name: %+v", tracks)
	}
	if tracks := reportedTracks([]byte(`{"type":"MEDIA_STATUS","status":[{"media":{"tracks":[{"trackId":1,"type":"TEXT"}]}}]}`), 0); tracks != nil {
		t.Fatalf("tracks of a status that names no media session, for a Play with none yet: %+v", tracks)
	}
	if tracks := reportedTracks([]byte(`{"type":"MEDIA_STATUS","status":[{"mediaSessionId":7}]}`), 7); tracks != nil {
		t.Fatalf("tracks from a status without media: %+v", tracks)
	}
}
