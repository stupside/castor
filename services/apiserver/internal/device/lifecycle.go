package device

import (
	"context"

	"github.com/stupside/castor/internal/machine"
)

// lifeState is where a playback stands between its hand-over and its end.
type lifeState uint8

const (
	// lifeStarting is a playback handed over that the device has not played yet.
	lifeStarting lifeState = iota
	// lifePlaying is a device that plays it, or holds it paused.
	lifePlaying
	// lifeEnded is a device that stopped after it played.
	lifeEnded
	// lifeRefused is a device that never played it.
	lifeRefused
	// lifeFailed is a device that broke while it played.
	lifeFailed
)

func (s lifeState) String() string {
	return [...]string{
		lifeStarting: "starting",
		lifePlaying:  "playing",
		lifeEnded:    "ended",
		lifeRefused:  "refused",
		lifeFailed:   "failed",
	}[s]
}

// lifeEvent is a fact about the playback that a device reported, however a family learns it.
type lifeEvent uint8

const (
	// devicePlayed is a device seen playing, or holding the stream paused.
	devicePlayed lifeEvent = iota
	// deviceStopped is a device gone idle, or its app closed.
	deviceStopped
	// deviceRefused is a device that will not play the stream.
	deviceRefused
	// deviceFailed is a device that failed with the stream playing.
	deviceFailed
)

func (e lifeEvent) String() string {
	return [...]string{
		devicePlayed:  "device-played",
		deviceStopped: "device-stopped",
		deviceRefused: "device-refused",
		deviceFailed:  "device-failed",
	}[e]
}

// Lifecycle follows one playback from its hand-over to its end, whatever the protocol that reports it: it starts,
// plays and ends, or it is refused before it plays, or the device fails while it plays. A family translates what its
// device says into these facts, and the Lifecycle says what they can mean: a device that is not playing yet stopping is
// no end, and nothing follows an end.
type Lifecycle struct {
	moves *machine.Machine[lifeState, lifeEvent]
}

// NewLifecycle follows a playback on a device of the family, which names the machine in traces.
func NewLifecycle(family Type) *Lifecycle {
	return &Lifecycle{moves: machine.New(string(family), lifeStarting, []lifeState{lifeEnded, lifeRefused, lifeFailed}, []machine.Edge[lifeState, lifeEvent]{
		{On: devicePlayed, From: []lifeState{lifeStarting, lifePlaying}, To: lifePlaying},
		{On: deviceStopped, From: []lifeState{lifePlaying}, To: lifeEnded},
		{On: deviceRefused, From: []lifeState{lifeStarting}, To: lifeRefused},
		{On: deviceFailed, From: []lifeState{lifePlaying}, To: lifeFailed},
	}, nil)}
}

// Played says the device plays the stream, or holds it paused.
func (l *Lifecycle) Played(ctx context.Context) { l.moves.Take(ctx, devicePlayed) }

// Stopped says the device went idle: that ends a playback that played, and means nothing to one that did not.
func (l *Lifecycle) Stopped(ctx context.Context) { l.moves.Take(ctx, deviceStopped) }

// Refused says the device will not play the stream.
func (l *Lifecycle) Refused(ctx context.Context) { l.moves.Take(ctx, deviceRefused) }

// Failed says the device failed while it played the stream.
func (l *Lifecycle) Failed(ctx context.Context) { l.moves.Take(ctx, deviceFailed) }

// Abandon ends the trace of a playback its owner is done with before it ended.
func (l *Lifecycle) Abandon(ctx context.Context) { l.moves.End(ctx) }

// Started is whether the device has played the stream.
func (l *Lifecycle) Started() bool {
	return l.moves.Is(lifePlaying) || l.moves.Is(lifeEnded) || l.moves.Is(lifeFailed)
}

// Over is a playback that ended, was refused, or failed.
func (l *Lifecycle) Over() bool {
	return l.moves.Is(lifeEnded) || l.moves.Is(lifeRefused) || l.moves.Is(lifeFailed)
}

// WasRefused is a playback the device never played.
func (l *Lifecycle) WasRefused() bool { return l.moves.Is(lifeRefused) }

// HasFailed is a playback the device broke while it played.
func (l *Lifecycle) HasFailed() bool { return l.moves.Is(lifeFailed) }
