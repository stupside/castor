package dlna

import (
	"context"
	"fmt"
	"time"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

func (s *session) Controls() device.Controls {
	return device.Controls{Pause: true, Seek: s.seekable.Load()}
}

func (s *session) Seek(ctx context.Context, at time.Duration) error {
	if at < 0 || !s.seekable.Load() {
		return device.ErrUnsupportedControl
	}
	// AVTransport's REL_TIME refers to the current track, including a paused one.
	target := fmt.Sprintf("%d:%02d:%02d", int64(at/time.Hour), int64(at/time.Minute)%60, int64(at/time.Second)%60)
	if err := s.action(ctx, "Seek", &struct{ InstanceID, Unit, Target string }{"0", "REL_TIME", target}); err != nil {
		return fmt.Errorf("seeking playback: %w", err)
	}
	return nil
}
