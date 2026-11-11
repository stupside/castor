package device

import (
	"context"
	"errors"
	"time"
)

// Controls is what the current player permits, separate from decoder capabilities.
type Controls struct{ Pause, Seek bool }

// Controller is implemented by protocols that can seek their current media session.
type Controller interface {
	Controls() Controls
	Seek(context.Context, time.Duration) error
}

var ErrUnsupportedControl = errors.New("the receiver does not support this playback control")

func ControlsOf(d Device) Controls {
	if c, ok := d.(Controller); ok {
		return c.Controls()
	}
	// Pause and Resume belong to the connected Device contract; seeking does not.
	return Controls{Pause: true}
}

func Seek(ctx context.Context, d Device, at time.Duration) error {
	if c, ok := d.(Controller); ok {
		return c.Seek(ctx, at)
	}
	return ErrUnsupportedControl
}

func (l *lease) Controls() Controls {
	if l.lifetime.Err() != nil {
		return Controls{}
	}
	return ControlsOf(l.device)
}

func (l *lease) Seek(ctx context.Context, at time.Duration) error {
	return l.command(ctx, func(ctx context.Context, d Device) error { return Seek(ctx, d, at) })
}

func (r *reconnecting) Controls() Controls {
	d, _ := r.current()
	return ControlsOf(d)
}

func (r *reconnecting) Seek(ctx context.Context, at time.Duration) error {
	d, _ := r.current()
	return Seek(ctx, d, at)
}
