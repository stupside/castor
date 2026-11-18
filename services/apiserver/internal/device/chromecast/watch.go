package chromecast

import (
	"context"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

// AwaitEnd observes the Cast channel rather than polling it.
func (s *session) AwaitEnd(ctx context.Context) error {
	s.watchMu.Lock()
	var e *device.Ending
	if s.watch != nil {
		e = s.watch.ending
	}
	s.watchMu.Unlock()
	return device.AwaitEnding(ctx, e, s.ch.gone, s.name, "the Cast connection closed while the cast was playing")
}

// follow makes p the Play the session follows, and gives up the one it followed.
func (s *session) follow(p *playback) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	s.watch.abandon()
	s.watch = p
}

// watchMessage lets the Play hear what the receiver says.
func (s *session) watchMessage(h messageHeader, payload []byte) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.watch != nil {
		s.watch.hear(h, payload)
	}
}
