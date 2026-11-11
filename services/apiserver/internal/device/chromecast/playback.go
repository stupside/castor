package chromecast

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	castmedia "github.com/vishen/go-chromecast/cast"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

// ending is one Play's end: done closes once the receiver says how that playback ended.
type ending struct {
	once sync.Once
	done chan struct{}
	err  error
}

func newEnding() *ending { return &ending{done: make(chan struct{})} }

// AwaitEnd observes the Cast channel rather than polling it.
func (s *session) AwaitEnd(ctx context.Context) error {
	s.watchMu.Lock()
	e := s.ending
	s.watchMu.Unlock()
	select {
	case <-e.done:
		return e.err
	case <-s.ch.gone:
		// A status read before the connection dropped still decides the ending.
		select {
		case <-e.done:
			return e.err
		default:
		}
		return &device.Gone{Device: s.name, Observed: "the Cast connection closed while the cast was playing"}
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *session) watchMessage(payload []byte) {
	var response castmedia.MediaStatusResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return
	}
	s.watchMu.Lock()
	if code, ok := receiverError(payload); ok && s.watch.armed {
		s.watch.errorCode = code
		slog.Warn("chromecast receiver reported an error", "detailed_error_code", code)
	}
	over, playErr := playbackOutcome(&s.watch, &response)
	var controls struct {
		Status []struct {
			MediaSessionID int     `json:"mediaSessionId"`
			Commands       *uint64 `json:"supportedMediaCommands"`
		} `json:"status"`
	}
	if response.Type == msgMediaStatus && json.Unmarshal(payload, &controls) == nil {
		for _, status := range controls.Status {
			if status.MediaSessionID == s.watch.mediaSession && status.Commands != nil {
				s.watch.commands = *status.Commands
			}
		}
	}
	if over {
		e := s.ending
		e.once.Do(func() {
			e.err = playErr
			close(e.done)
		})
	}
	s.watchMu.Unlock()
}

// playback follows the one Play the device was last handed, from its load to its end.
type playback struct {
	armed        bool
	content      string
	mediaSession int
	active       bool
	commands     uint64
	errorCode    int // the detailed code of the receiver's last ERROR message, 0 when it sent none
}

func (w *playback) begin(content string) {
	w.armed = true
	w.content = content
	w.mediaSession = 0
	w.active = false
	w.commands = 0
	w.errorCode = 0
}

func (w *playback) disarm() {
	w.armed = false
	w.content = ""
	w.mediaSession = 0
	w.active = false
	w.commands = 0
	w.errorCode = 0
}

// named adds the receiver's own error code, when it sent one, to the error a playback ends with.
func (w *playback) named(err error) error {
	if w.errorCode == 0 || err == nil {
		return err
	}
	return fmt.Errorf("%w (receiver detailed error code %d)", err, w.errorCode)
}

// failure names the receiver's own error code when it sent one.
func (w *playback) failure(text string) error {
	return w.named(errors.New(text))
}

func (w *playback) observe(status castmedia.Media) (bool, error) {
	switch {
	case w.mediaSession == 0:
		if status.Media.ContentId != w.content || status.MediaSessionId == 0 {
			return false, nil
		}
		w.mediaSession = status.MediaSessionId
	case status.MediaSessionId != w.mediaSession:
		return false, nil
	}

	switch status.PlayerState {
	case "BUFFERING", "PLAYING", "PAUSED":
		w.active = true
	case stateIdle:
		switch status.IdleReason {
		case idleError:
			if w.active {
				return true, w.failure("chromecast playback ended with receiver error")
			}
			return true, w.failure("chromecast refused the media: the receiver went idle with an error before playback began")
		case "FINISHED", "CANCELLED", "INTERRUPTED":
			return w.active, nil
		}
	}
	return false, nil
}

func playbackOutcome(w *playback, response *castmedia.MediaStatusResponse) (bool, error) {
	if !w.armed {
		return false, nil
	}
	switch response.Type {
	case msgClose:
		return w.active, nil
	case msgMediaStatus:
		for _, status := range response.Status {
			if over, err := w.observe(status); over {
				return true, err
			}
		}
		return false, nil
	default:
		return false, nil
	}
}

const msgError = "ERROR"

// receiverError reads the detailed code of a receiver's ERROR message.
func receiverError(payload []byte) (code int, ok bool) {
	var message struct {
		Type string `json:"type"`
		Code *int   `json:"detailedErrorCode"`
	}
	if err := json.Unmarshal(payload, &message); err != nil || message.Type != msgError || message.Code == nil {
		return 0, false
	}
	return *message.Code, true
}
