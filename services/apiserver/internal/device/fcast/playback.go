package fcast

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

// The playback states a PlaybackUpdate reports.
const (
	stateIdle    = 0
	statePlaying = 1
	statePaused  = 2
)

// eventMediaItemEnd is the event a receiver sends when an item plays to its end.
const eventMediaItemEnd = 1

const (
	// Receivers report a failure as Idle, then the error; an Idle only ends playback once no error has followed it.
	idleSettle = 2 * time.Second

	// A receiver that never plays may be waiting on its viewer to allow it, so the cast does not wait on it forever.
	startupTimeout = 30 * time.Second

	// Anything past a year is no position in a cast, and would overflow a duration.
	maxPosition = 365 * 24 * time.Hour
)

type playUpdateMessage struct {
	PlayData *struct {
		URL string `json:"url"`
	} `json:"playData"`
}

type playbackUpdateMessage struct {
	State int      `json:"state"`
	Time  *float64 `json:"time"`
}

type playbackErrorMessage struct {
	Message string `json:"message"`
}

type eventMessage struct {
	Event struct {
		Type int `json:"type"`
		Item struct {
			URL string `json:"url"`
		} `json:"item"`
	} `json:"event"`
}

// ending is one Play's end: done closes once the receiver says how that playback ended.
type ending struct {
	once sync.Once
	done chan struct{}
	err  error
}

func (e *ending) finish(err error) {
	e.once.Do(func() {
		e.err = err
		close(e.done)
	})
}

// playback follows one Play: only what the receiver says after echoing it back is this Play's.
type playback struct {
	url string
	// verdict carries the receiver's answer to the Play: nil once it echoes the URL, else the error it sent first.
	verdict  chan error
	answered bool

	loaded, active, idle bool
	// afterIdle is whether the last update before the echo was Idle.
	afterIdle bool
	// head is the last position reported, placed while it stands.
	head   device.Playhead
	placed bool

	ending *ending
}

func newPlayback(url string) *playback {
	return &playback{url: url, verdict: make(chan error, 1), ending: &ending{done: make(chan struct{})}}
}

func (p *playback) answer(err error) {
	if !p.answered {
		p.answered = true
		p.verdict <- err
	}
}

// observe applies one receiver message, reporting whether it left playback idle after it had played.
func (p *playback) observe(op opcode, body []byte) bool {
	switch op {
	case opPlayUpdate:
		var update playUpdateMessage
		if json.Unmarshal(body, &update) != nil || update.PlayData == nil {
			return false
		}
		switch {
		case update.PlayData.URL == p.url:
			p.loaded = true
			p.answer(nil)
		case p.loaded:
			// Another sender's media took the receiver.
			p.ending.finish(nil)
		}
	case opPlaybackUpdate:
		var update playbackUpdateMessage
		if json.Unmarshal(body, &update) != nil {
			return false
		}
		// Before the echo, updates still describe what played before.
		if !p.loaded {
			p.afterIdle = update.State == stateIdle
			return false
		}
		switch update.State {
		case statePlaying, statePaused:
			p.active, p.idle = true, false
			p.head, p.placed = playhead(update)
		case stateIdle:
			p.placed = false
			if p.active && !p.idle {
				p.idle = true
				return true
			}
		}
	case opPlaybackError:
		var failure playbackErrorMessage
		_ = json.Unmarshal(body, &failure)
		switch {
		case !p.loaded && p.afterIdle:
			// Receivers fail media as Idle then the error; the desktop one refuses a Play with the error alone.
		case !p.loaded:
			p.answer(fmt.Errorf("the fcast receiver refused the media: %s", failure.Message))
		case p.active:
			p.ending.finish(&device.PlaybackError{Err: fmt.Errorf("fcast playback failed: %s", failure.Message)})
		default:
			p.ending.finish(&device.PlaybackError{Err: fmt.Errorf("the fcast receiver refused the media: %s", failure.Message)})
		}
	case opEvent:
		var event eventMessage
		if p.loaded && json.Unmarshal(body, &event) == nil && event.Event.Type == eventMediaItemEnd && event.Event.Item.URL == p.url {
			p.ending.finish(nil)
		}
	}
	return false
}

func playhead(update playbackUpdateMessage) (device.Playhead, bool) {
	if update.Time == nil || !(*update.Time >= 0 && *update.Time <= maxPosition.Seconds()) {
		return device.Playhead{}, false
	}
	return device.Playhead{At: time.Duration(*update.Time * float64(time.Second)), Paused: update.State == statePaused}, true
}

// AwaitEnd observes the FCast connection rather than polling it.
func (s *session) AwaitEnd(ctx context.Context) error {
	s.mu.Lock()
	p := s.current
	s.mu.Unlock()
	if p == nil {
		return errors.New("nothing has been played on this fcast receiver")
	}
	startup := time.NewTimer(startupTimeout)
	defer startup.Stop()
	for {
		select {
		case <-p.ending.done:
			return p.ending.err
		case <-s.ch.gone:
			// An end read before the connection dropped still decides it.
			select {
			case <-p.ending.done:
				return p.ending.err
			default:
			}
			return &device.Gone{Device: s.name, Observed: "the FCast connection closed while the cast was playing"}
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-startup.C:
			s.mu.Lock()
			active := p.active
			s.mu.Unlock()
			if !active {
				return &device.PlaybackError{Err: fmt.Errorf("the fcast receiver did not start playback within %s", startupTimeout)}
			}
		}
	}
}
