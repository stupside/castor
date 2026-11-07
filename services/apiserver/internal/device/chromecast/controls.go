package chromecast

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/stupside/castor/services/apiserver/internal/device"
	castmedia "github.com/vishen/go-chromecast/cast"
)

func (s *session) Controls() device.Controls {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	return device.Controls{Pause: s.watch.commands&1 != 0, Seek: s.watch.commands&2 != 0}
}

type seekCommand struct {
	castmedia.MediaHeader
	CurrentTime float64 `json:"currentTime"`
	ResumeState string  `json:"resumeState,omitempty"`
}

// A finite HLS LOAD can be accepted while the receiver stays buffering at zero.
// Keep the previous publication until playback starts. One seek into available
// media recovers receivers stuck at the beginning of the new portion.
func (s *session) awaitHLSStart(ctx context.Context, reply []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	s.watchMu.Lock()
	transport, content := s.transport, s.watch.content
	s.watchMu.Unlock()
	started, nudged := time.Now(), false
	previousState, previousReason := "", ""
	for {
		if err := loadVerdict(reply, content); err != nil {
			return err
		}
		// LOADING may arrive as IDLE before a media session is assigned.
		// Refresh the identity after each reply rather than pinning its initial zero.
		s.watchMu.Lock()
		mediaSession := s.watch.mediaSession
		s.watchMu.Unlock()
		status, err := positionStatus(reply, mediaSession)
		if err == nil && (status.PlayerState != previousState || status.IdleReason != previousReason) {
			slog.DebugContext(ctx, "waiting for HLS playback", "state", status.PlayerState, "idle_reason", status.IdleReason, "media_session", mediaSession)
			previousState, previousReason = status.PlayerState, status.IdleReason
		}
		switch {
		case err == nil && (status.PlayerState == "PLAYING" || status.PlayerState == "PAUSED"):
			return nil
		case err == nil && status.PlayerState == stateIdle && status.IdleReason != "":
			return fmt.Errorf("the receiver ended HLS playback before it started (%s)", status.IdleReason)
		}
		if err == nil && mediaSession != 0 && !nudged && status.PlayerState == "BUFFERING" && time.Since(started) >= 3*time.Second && status.CurrentTime != nil && *status.CurrentTime == 0 && status.Seekable != nil && status.Seekable.Start == 0 && status.Seekable.End >= 2 && s.Controls().Seek {
			nudged = true
			slog.DebugContext(ctx, "recovering HLS playback buffered at zero", "at", time.Second)
			reply, err = s.ch.request(ctx, transport, nsMedia, &seekCommand{
				Type: "SEEK", MediaSessionId: mediaSession,
				CurrentTime: 1, ResumeState: "PLAYBACK_START",
			})
			if err != nil {
				return fmt.Errorf("starting buffered HLS playback: %w", err)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the receiver did not start HLS playback (state %s): %w", status.PlayerState, context.Cause(ctx))
		case <-time.After(100 * time.Millisecond):
		}
		// GET_STATUS needs no media session ID while the LOAD is still assigning it.
		reply, err = s.ch.request(ctx, transport, nsMedia, &castmedia.PayloadHeader{Type: msgGetStatus})
		if err != nil {
			return err
		}
	}
}

func (s *session) Seek(ctx context.Context, at time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	s.watchMu.Lock()
	transport, mediaSession, content := s.transport, s.watch.mediaSession, s.watch.content
	s.watchMu.Unlock()
	if mediaSession == 0 {
		return errNoMediaSession
	}
	// Omitting resumeState preserves the receiver's current paused/playing state.
	reply, err := s.ch.request(ctx, transport, nsMedia, &seekCommand{
		Type: "SEEK", MediaSessionId: mediaSession,
		CurrentTime: at.Seconds(),
	})
	if err == nil {
		err = loadVerdict(reply, content)
	}
	if err != nil {
		return fmt.Errorf("seeking chromecast playback: %w", err)
	}
	// An accepted SEEK is not proof the decoder reached it. Unseekable growing
	// MP4s acknowledge the command but restart at zero; do not queue more of those.
	for {
		head, err := playhead(reply, mediaSession)
		if err != nil {
			return err
		}
		if delta := head.At - at; delta >= -2*time.Second && delta <= 2*time.Second {
			slog.DebugContext(ctx, "chromecast seek confirmed", "asked", at, "at", head.At, "paused", head.Paused)
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the receiver did not reach %s (last position %s); this position may not be available yet: %w", at, head.At, context.Cause(ctx))
		case <-time.After(100 * time.Millisecond):
		}
		_, reply, err = s.mediaRequest(ctx, msgGetStatus)
		if err != nil {
			return err
		}
		if err := loadVerdict(reply, content); err != nil {
			return err
		}
	}
}
