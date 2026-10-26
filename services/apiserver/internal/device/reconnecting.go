package device

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
)

// stopTimeout bounds the stop a closing cast sends its device.
const stopTimeout = 3 * time.Second

// reconnecting is the device a cast lends, so later attempts still reach it after its connection is gone.
type reconnecting struct {
	dir   *Directory
	owner *lease
	cur   atomic.Pointer[link]
}

// link is a connected device and the address it was reached at.
type link struct {
	device Device
	target Info
}

func (r *reconnecting) current() (Device, Info) {
	l := r.cur.Load()
	return l.device, l.target
}

func (r *reconnecting) Play(ctx context.Context, playback Playback) error {
	dev, target := r.current()
	err := dev.Play(ctx, playback)
	if _, gone := errors.AsType[*Gone](err); !gone {
		return err
	}
	fresh, at, cerr := r.dir.dial(ctx, target)
	if cerr != nil {
		return fmt.Errorf("%w; connecting again: %w", err, cerr)
	}
	if r.owner != nil {
		if err := r.owner.claimResolved(ctx, at); err != nil {
			_ = fresh.Close()
			return err
		}
	}
	stale := r.cur.Swap(&link{device: fresh, target: at})
	_ = stale.device.Close()
	return fresh.Play(ctx, playback)
}

func (r *reconnecting) AwaitEnd(ctx context.Context) error {
	dev, _ := r.current()
	return dev.AwaitEnd(ctx)
}

func (r *reconnecting) Pause(ctx context.Context) error {
	dev, _ := r.current()
	return dev.Pause(ctx)
}

func (r *reconnecting) Resume(ctx context.Context) error {
	dev, _ := r.current()
	return dev.Resume(ctx)
}

func (r *reconnecting) Stop(ctx context.Context) error {
	dev, _ := r.current()
	return dev.Stop(ctx)
}

func (r *reconnecting) Position(ctx context.Context) (Playhead, error) {
	dev, _ := r.current()
	return dev.Position(ctx)
}

func (r *reconnecting) Capabilities() *mediav1.Capabilities {
	dev, _ := r.current()
	return dev.Capabilities()
}

// Close stops the device, then lets go of it; a device already gone or stopped has nothing left to stop.
func (r *reconnecting) Close() error {
	stopping, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	if err := r.Stop(stopping); err != nil {
		slog.Debug("stopping the device on close", "error", err)
	}
	dev, _ := r.current()
	return dev.Close()
}
