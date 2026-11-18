package chromecast

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"

	castmedia "github.com/vishen/go-chromecast/cast"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

var (
	errRefused = errors.New("chromecast refused the media: the receiver went idle with an error before playback began")
	errFailed  = errors.New("chromecast playback ended with receiver error")
)

// captionsState is a playlist's own caption track, from the playlist listing it to the receiver being asked to show it.
type captionsState uint8

const (
	// noOwnCaptions is a playlist that lists no caption track of its own.
	noOwnCaptions captionsState = iota
	// captionsListed is a track the receiver has not been asked to show.
	captionsListed
	// captionsAsked is a track the receiver was asked to show, once, so a viewer who turns it off keeps it off.
	captionsAsked
)

// playback follows the one Play the device was last handed, from its LOAD to its end. What the receiver says becomes
// the facts of the device.Lifecycle every family shares; what is the Cast protocol's own stays here: which media
// session is the Play's, the code the receiver gave for an error, and the caption track to show.
// The session holds its lock around every use of it.
type playback struct {
	content string
	// transport is the media receiver the Play loaded on, where its media commands go.
	transport string
	// mediaSession is the receiver's session for the media, zero until it reports it.
	mediaSession int
	// receiverCode is the detailed code of the last error the receiver sent, zero when it sent none.
	receiverCode int
	captions     captionsState

	life   *device.Lifecycle
	ending *device.Ending
	// identity carries the cast's id into the lifecycle's trace: the receiver's messages come from no call of the cast's.
	identity context.Context
}

// newPlayback starts following a Play of content loaded on transport, whose playlist lists captions of its own or not.
func newPlayback(ctx context.Context, content, transport string, listsCaptions bool) *playback {
	captions := noOwnCaptions
	if listsCaptions {
		captions = captionsListed
	}
	return &playback{
		content: content, transport: transport, captions: captions,
		life: device.NewLifecycle(familyType), ending: device.NewEnding(), identity: context.WithoutCancel(ctx),
	}
}

// abandon ends the trace of a Play its session is done with before it ended; a session with no Play has none to end.
func (p *playback) abandon() {
	if p != nil {
		p.life.Abandon(p.identity)
	}
}

// outcome is the error the Play ended on, nil for a clean end.
func (p *playback) outcome() error {
	switch {
	case p.life.WasRefused():
		return p.named(errRefused)
	case p.life.HasFailed():
		return p.named(errFailed)
	}
	return nil
}

// named is err with the receiver's own code for why it failed, when it gave one.
func (p *playback) named(err error) error {
	if err == nil || p.receiverCode == 0 {
		return err
	}
	return fmt.Errorf("%w (receiver error code %d)", err, p.receiverCode)
}

// hear folds one receiver message into the Play, and returns the command the Play now asks the receiver for.
func (p *playback) hear(h messageHeader, payload []byte) (editTracksInfo, bool) {
	if h.DetailedErrorCode != 0 {
		p.receiverCode = h.DetailedErrorCode
	}
	var response castmedia.MediaStatusResponse
	if json.Unmarshal(payload, &response) != nil {
		return editTracksInfo{}, false
	}
	p.apply(&response)
	return p.showCaptions(reportedTracks(payload, p.mediaSession))
}

// apply turns a message into what it says of the device, and ends the Play once that ends it.
func (p *playback) apply(response *castmedia.MediaStatusResponse) {
	switch response.Type {
	case msgClose:
		p.life.Stopped(p.identity)
	case msgMediaStatus:
		for _, status := range response.Status {
			p.observe(status)
		}
	}
	if p.life.Over() {
		p.ending.Finish(p.outcome())
	}
}

// observe turns one status into what it says of the device, if it is of this Play's media.
func (p *playback) observe(status castmedia.Media) {
	if p.mediaSession == 0 {
		if status.Media.ContentId != p.content || status.MediaSessionId == 0 {
			return
		}
		p.mediaSession = status.MediaSessionId
	} else if status.MediaSessionId != p.mediaSession {
		return
	}
	switch status.PlayerState {
	case stateBuffering, statePlaying, statePaused:
		p.life.Played(p.identity)
	case stateIdle:
		switch status.IdleReason {
		case idleError:
			if p.life.Started() {
				p.life.Failed(p.identity)
			} else {
				p.life.Refused(p.identity)
			}
		case idleFinished, idleCancelled, idleInterrupted:
			p.life.Stopped(p.identity)
		}
	}
}

// showCaptions is the command that shows the playlist's own caption track once the receiver reports it.
func (p *playback) showCaptions(tracks []track) (editTracksInfo, bool) {
	if p.captions != captionsListed {
		return editTracksInfo{}, false
	}
	for _, t := range tracks {
		if t.Type == trackText {
			p.captions = captionsAsked
			return editTracksInfo{Type: msgEditTracks, MediaSessionID: p.mediaSession, ActiveTrackIDs: []int{t.TrackID}}, true
		}
	}
	return editTracksInfo{}, false
}
