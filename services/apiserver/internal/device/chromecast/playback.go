package chromecast

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
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

func (s *session) watchMessage(h messageHeader, payload []byte) {
	var response castmedia.MediaStatusResponse
	decoded := json.Unmarshal(payload, &response)
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if h.DetailedErrorCode != 0 {
		s.watch.receiverCode = h.DetailedErrorCode
	}
	if decoded != nil {
		return
	}
	if over, playErr := playbackOutcome(&s.watch, &response); over {
		e := s.ending
		e.once.Do(func() {
			e.err = s.watch.named(playErr)
			close(e.done)
		})
	}
}

// playback follows the one Play the device was last handed, from its load to its end.
type playback struct {
	armed        bool
	content      string
	mediaSession int
	active       bool
	// receiverCode is the detailed code of the last error the receiver sent since begin, zero when it sent none.
	receiverCode int
}

func (w *playback) begin(content string) { *w = playback{armed: true, content: content} }

func (w *playback) disarm() { *w = playback{} }

// named is err with the receiver's own code for why it failed, when it gave one.
func (w *playback) named(err error) error {
	if err == nil || w.receiverCode == 0 {
		return err
	}
	return fmt.Errorf("%w (receiver error code %d)", err, w.receiverCode)
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
				return true, errors.New("chromecast playback ended with receiver error")
			}
			return true, errors.New("chromecast refused the media: the receiver went idle with an error before playback began")
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
