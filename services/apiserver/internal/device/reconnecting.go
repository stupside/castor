package device

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync/atomic"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
)

// reconnecting is the device a cast lends, so later attempts still reach it after its connection is gone.
type reconnecting struct {
	dir *Directory
	cur atomic.Pointer[link]
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

func (r *reconnecting) Play(ctx context.Context, streamURL *url.URL, container mediav1.Container) error {
	dev, target := r.current()
	err := dev.Play(ctx, streamURL, container)
	if _, gone := errors.AsType[*Gone](err); !gone {
		return err
	}
	fresh, at, cerr := r.dir.dial(ctx, target)
	if cerr != nil {
		return fmt.Errorf("%w; connecting again: %w", err, cerr)
	}
	stale := r.cur.Swap(&link{device: fresh, target: at})
	_ = stale.device.Close()
	return fresh.Play(ctx, streamURL, container)
}

func (r *reconnecting) AwaitEnd(ctx context.Context) error {
	dev, _ := r.current()
	return dev.AwaitEnd(ctx)
}

func (r *reconnecting) Capabilities() *mediav1.Capabilities {
	dev, _ := r.current()
	return dev.Capabilities()
}

func (r *reconnecting) Close() error {
	dev, _ := r.current()
	return dev.Close()
}
