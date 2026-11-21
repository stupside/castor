package execute

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// headDevice is a device that says where it plays, or that it cannot.
type headDevice struct {
	Device
	at    time.Duration
	known bool
}

func (d *headDevice) Position(context.Context) (time.Duration, bool, bool) {
	return d.at, false, d.known
}

func TestAnEncodeThatFailedHavingPlayedUnwatchedIsPlacedWhereTheDeviceSaysItGot(t *testing.T) {
	encoderFailed := errors.New("encoder: signal: killed")
	played := recovery.Evidence{Reached: health.Playing, EncodeErr: encoderFailed}
	live := recovery.Attempt{Program: media.Program{Inputs: []media.Input{{Fetch: media.Fetch{Live: true}}}}}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tt := range []struct {
		name    string
		ctx     context.Context
		dev     Device
		attempt recovery.Attempt
		e       recovery.Evidence
		placed  bool
		at      time.Duration
	}{
		{"a device that says where it plays", t.Context(), &headDevice{at: 95 * time.Second, known: true}, recovery.Attempt{}, played, true, 95 * time.Second},
		{"a device that cannot say", t.Context(), &headDevice{known: false}, recovery.Attempt{}, played, false, 0},
		{"a device that says it is at the start", t.Context(), &headDevice{known: true}, recovery.Attempt{}, played, false, 0},
		{"a device with no playhead", t.Context(), &fakeDevice{}, recovery.Attempt{}, played, false, 0},
		{"a failure that is not the encode's", t.Context(), &headDevice{at: time.Minute, known: true}, recovery.Attempt{}, recovery.Evidence{Reached: health.Playing}, false, 0},
		{"an attempt that never played", t.Context(), &headDevice{at: time.Minute, known: true}, recovery.Attempt{}, recovery.Evidence{Reached: health.Opening, EncodeErr: encoderFailed}, false, 0},
		{"a live program, which resumes at its edge", t.Context(), &headDevice{at: time.Minute, known: true}, live, played, false, 0},
		{"a device that went away", t.Context(), &headDevice{at: time.Minute, known: true}, recovery.Attempt{}, recovery.Evidence{Reached: health.Playing, EncodeErr: encoderFailed, DeviceGone: &media.Gone{Device: "salon"}}, false, 0},
		{"a cast the viewer stopped", cancelled, &headDevice{at: time.Minute, known: true}, recovery.Attempt{}, played, false, 0},
		{"a place a watch already kept", t.Context(), &headDevice{at: time.Minute, known: true}, recovery.Attempt{}, recovery.Evidence{Reached: health.Playing, EncodeErr: encoderFailed, Vitals: health.Vitals{}.WithHead(7 * time.Second)}, true, 7 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := withPlace(tt.ctx, tt.dev, tt.attempt, tt.e)
			at, placed := got.Vitals.Reached()
			if placed != tt.placed || at != tt.at {
				t.Errorf("the viewer is placed at %s (%t), want %s (%t)", at, placed, tt.at, tt.placed)
			}
		})
	}
}
