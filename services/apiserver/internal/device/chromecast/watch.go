package chromecast

import (
	"context"
	"log/slog"

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

// watchMessage lets the Play hear what the receiver says, and sends what the Play then asks the receiver for.
func (s *session) watchMessage(h messageHeader, payload []byte) {
	transport, show, ok := s.hear(h, payload)
	if !ok {
		return
	}
	slog.Info("asking the chromecast to show the captions", "track", show.ActiveTrackIDs[0])
	// Sent off the reader, which a write would hold up while the receiver is writing to it too; the send bounds its own wait.
	go func() {
		if err := s.ch.post(context.Background(), transport, nsMedia, &show); err != nil {
			slog.Warn("the chromecast was not asked to show the captions", "error", err)
		}
	}()
}

// hear gives the Play a message under the session's lock, with where what it asks for goes.
func (s *session) hear(h messageHeader, payload []byte) (transport string, show editTracksInfo, ok bool) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.watch == nil {
		return "", editTracksInfo{}, false
	}
	show, ok = s.watch.hear(h, payload)
	return s.watch.transport, show, ok
}
