package execute

import (
	"context"

	"github.com/stupside/castor/internal/machine"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
)

// attemptEvent keeps this machine's events distinct at Go call sites.
type attemptEvent uint8

const (
	inspectionStarted attemptEvent = iota
	inspectionFinished
	planFinished
	productionStarted
	presentationReady
	playbackRequested
	playbackStarted
	productionFinished
	playbackEnded
	deliverySettled
	healthEnded
	drainElapsed
	deliverySought
	attemptCancelled
	attemptFailed
	cleanupFinished
)

func (e attemptEvent) String() string {
	return [...]string{
		inspectionStarted:  "inspection-started",
		inspectionFinished: "inspection-finished",
		planFinished:       "plan-finished",
		productionStarted:  "production-started",
		presentationReady:  "presentation-ready",
		playbackRequested:  "playback-requested",
		playbackStarted:    "playback-started",
		productionFinished: "production-finished",
		playbackEnded:      "playback-ended",
		deliverySettled:    "delivery-settled",
		healthEnded:        "health-ended",
		drainElapsed:       "drain-elapsed",
		deliverySought:     "delivery-sought",
		attemptCancelled:   "cancelled",
		attemptFailed:      "failed",
		cleanupFinished:    "cleanup-finished",
	}[e]
}

type attemptState uint8

const (
	preparing attemptState = iota
	inspecting
	planning
	outputPlanned
	producing
	ready
	startingPlayback
	playingBack
	// draining is a device that took the whole stream and plays out what it holds.
	draining
	stopping
	attemptFinished
)

func (s attemptState) String() string {
	return [...]string{
		preparing:        "preparing",
		inspecting:       "inspecting",
		planning:         "planning",
		outputPlanned:    "planned",
		producing:        "producing",
		ready:            "ready",
		startingPlayback: "starting-playback",
		playingBack:      "playing",
		draining:         "draining",
		stopping:         "stopping",
		attemptFinished:  "finished",
	}[s]
}

// Callbacks update state-local evidence. Operations and cleanup remain owned
// by the coordinator outside FSM callbacks.
func (c *coordinator) attemptMachine() *machine.Machine[attemptState, attemptEvent] {
	active := []attemptState{preparing, inspecting, planning, outputPlanned, producing, ready, startingPlayback, playingBack, draining}
	return machine.New("attempt", preparing, []attemptState{attemptFinished}, []machine.Edge[attemptState, attemptEvent]{
		{On: inspectionStarted, From: []attemptState{preparing}, To: inspecting},
		{On: inspectionFinished, From: []attemptState{inspecting}, To: planning},
		{On: planFinished, From: []attemptState{planning}, To: outputPlanned},
		{On: productionStarted, From: []attemptState{outputPlanned}, To: producing},
		{On: presentationReady, From: []attemptState{outputPlanned, producing}, To: ready},
		{On: playbackRequested, From: []attemptState{ready}, To: startingPlayback},
		{On: playbackStarted, From: []attemptState{startingPlayback}, To: playingBack},
		{On: productionFinished, From: []attemptState{startingPlayback}, To: startingPlayback},
		{On: productionFinished, From: []attemptState{playingBack}, To: playingBack},
		{On: productionFinished, From: []attemptState{draining}, To: draining},
		{On: playbackEnded, From: []attemptState{playingBack, draining}, To: stopping},
		{On: deliverySettled, From: []attemptState{playingBack}, To: draining},
		{On: healthEnded, From: []attemptState{playingBack}, To: stopping},
		{On: drainElapsed, From: []attemptState{draining}, To: stopping},
		{On: deliverySought, From: []attemptState{playingBack, draining}, To: playingBack},
		{On: attemptCancelled, From: active, To: stopping},
		{On: attemptFailed, From: active, To: stopping},
		{On: cleanupFinished, From: []attemptState{stopping}, To: attemptFinished},
	}, map[attemptEvent]func(context.Context){
		attemptCancelled: func(context.Context) { c.interruption = context.Cause(c.ctx) },
		playbackEnded: func(context.Context) {
			// Capture starvation before cleanup changes the producer's completion.
			if c.presentation.delivery != nil {
				if made, sent, ok := starved(c.presentation.feed, *c.presentation.delivery); ok {
					c.starvation = health.DeviceStarved("the playing cast", made, sent)
				}
			}
		},
		playbackStarted: func(context.Context) { c.presentation.reached.reached = health.Playing },
	})
}
