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
	checkPlayback := s.watch.finiteMP4Source && s.watch.playerState == "PLAYING" && s.watch.commands&1 != 0
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
	// Keep follow-up commands on the session that received the SEEK, even if a new LOAD starts.
	command := func(kind string) ([]byte, error) {
		answer, err := s.ch.request(ctx, transport, nsMedia, &castmedia.MediaHeader{Type: kind, MediaSessionId: mediaSession})
		if err == nil {
			err = loadVerdict(answer, content)
		}
		return answer, err
	}
	// An accepted SEEK is not proof the decoder reached it. Unseekable growing
	// MP4s acknowledge the command but restart at zero; do not queue more of those.
	var bufferingSince time.Time
	var lastAt time.Duration
	var atTarget, recovered bool
	for {
		status, err := positionStatus(reply, mediaSession)
		if err != nil || status.CurrentTime == nil {
			return device.ErrNoPosition
		}
		current := time.Duration(*status.CurrentTime * float64(time.Second))
		near := current >= at-2*time.Second && current <= at+2*time.Second
		if near && (!checkPlayback || status.PlayerState == "PAUSED" && !recovered || status.IdleReason == "FINISHED" ||
			status.PlayerState == "PLAYING" && atTarget && current > lastAt) {
			slog.DebugContext(ctx, "chromecast seek confirmed", "asked", at, "at", current, "state", status.PlayerState)
			return nil
		}
		// A Cast receiver can echo the new MP4 position and stay BUFFERING there.
		// Cycle pause/play once only if its clock is stationary at the requested point.
		if checkPlayback && !recovered && near && status.PlayerState == "BUFFERING" {
			if bufferingSince.IsZero() || current != lastAt {
				bufferingSince = time.Now()
			} else if time.Since(bufferingSince) >= time.Second {
				slog.DebugContext(ctx, "resuming MP4 buffered at the seek position", "at", current)
				if _, err := command(msgPause); err != nil {
					return err
				}
				if _, err := command(msgPlay); err != nil {
					return err
				}
				recovered = true
			}
		} else {
			bufferingSince = time.Time{}
		}
		atTarget, lastAt = near, current
		select {
		case <-ctx.Done():
			return fmt.Errorf("the receiver did not reach %s (last position %s); this position may not be available yet: %w", at, current, context.Cause(ctx))
		case <-time.After(100 * time.Millisecond):
		}
		reply, err = command(msgGetStatus)
		if err != nil {
			return err
		}
	}
}
