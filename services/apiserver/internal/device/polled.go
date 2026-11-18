package device

import (
	"context"
	"time"
)

// Phase is what a poll says of the stream a device was handed.
type Phase uint8

const (
	// Settling is a device between states: loading, buffering, switching.
	Settling Phase = iota
	// Holding is a device that plays the stream or holds it paused.
	Holding
	// Idle is a device holding no stream.
	Idle
)

// Reading is a device's answer to one poll, in the terms every family shares.
type Reading struct {
	Phase Phase
	// Observed is what the device said, for a message that names it.
	Observed string
	// Fault is why the device says its playback failed. It counts only if this poll ends the playback,
	// since a status read before the end may be left from an earlier playback.
	Fault *PlaybackError
}

// Patience is how long a device may go without playing the stream before it is taken to have refused it.
type Patience interface {
	// Spent is whether the wait ran out, given what the device said on this poll. It is asked only while the device has not played.
	Spent(Reading) bool
}

type idlePolls struct{ limit, streak int }

// IdlePolls is a device that may sit idle for limit polls in a row.
func IdlePolls(limit int) Patience { return &idlePolls{limit: limit} }

func (p *idlePolls) Spent(r Reading) bool {
	if r.Phase == Idle {
		p.streak++
	} else {
		p.streak = 0
	}
	return p.streak >= p.limit
}

type within struct {
	limit time.Duration
	began time.Time
}

// Within is a device that may take limit from now to play.
func Within(limit time.Duration) Patience { return &within{limit: limit, began: time.Now()} }

func (p *within) Spent(Reading) bool { return time.Since(p.began) >= p.limit }

// Polled is what a family implements when its device is asked about its playback rather than telling it: the three
// things only the protocol knows. The cast's end, a refusal, and a failure follow from them, the same for every family.
type Polled interface {
	// Read asks the device where its playback stands. An answer that is a *PlaybackError ends the wait with it.
	Read(ctx context.Context) (Reading, error)
	// Patience is how long a device may go without playing; each playback gets its own.
	Patience() Patience
	// Refusal is the error a playback the device never played ends with, given the device's last answer.
	Refusal(last Reading) error
}

// AwaitPlayback polls a device of the family until its playback ends. A device that played and went idle ended it,
// one that never played within its patience refused it, and a fault read as the playback ends says why it failed.
func AwaitPlayback(ctx context.Context, family Type, name, question string, p Polled) error {
	life := NewLifecycle(family)
	defer life.Abandon(ctx)
	patience := p.Patience()
	var last Reading
	err := AwaitPolledEnd(ctx, name, question, func(ctx context.Context) (bool, error) {
		reading, err := p.Read(ctx)
		if err != nil {
			return false, err
		}
		last = reading
		switch {
		case reading.Phase == Holding:
			life.Played(ctx)
		case reading.Phase == Idle && life.Started():
			life.Stopped(ctx)
		}
		if !life.Started() && patience.Spent(reading) {
			life.Refused(ctx)
		}
		if life.Over() && reading.Fault != nil {
			return false, reading.Fault
		}
		return life.Over(), nil
	})
	if err == nil && life.WasRefused() {
		return p.Refusal(last)
	}
	return err
}
