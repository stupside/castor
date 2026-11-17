package recovery

import (
	"errors"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// The row for a read its own encoder failed (classify.go, execute/hardware_errors.go).
func TestAReadItsHardwareEncoderFailedIsMadeAgainInSoftware(t *testing.T) {
	failed := errors.New("upstream pull: exit status 187")
	for _, tt := range []struct {
		name string
		in   Evidence
	}{
		{"having landed nothing", Evidence{Reached: health.Reading, Verdict: health.Dead, ReadErr: failed, ReadExit: 187, EncodeErr: failed, Hardware: true}},
		{"over a sound it copied", Evidence{Reached: health.Reading, Verdict: health.Dead, ReadErr: failed, ReadExit: 187, Copied: media.Axes{Audio: true}, EncodeErr: failed, Hardware: true, Vitals: health.Vitals{Landed: 4 << 10, Samples: 2}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := classFor(tt.in).kind; got != encodeFailed {
				t.Errorf("classified %s, want %s", got, encodeFailed)
			}
			if next, ok := softwareEncode.apply(t.Context(), change{outcome: Outcome{Evidence: tt.in}}); !ok || !next.Software {
				t.Errorf("software encode offered %t (software %t), want a software attempt", ok, next.Software)
			}
		})
	}
	// A read that failed with no word from its encoder keeps the verdict it had.
	if got := classFor(Evidence{Reached: health.Reading, Verdict: health.Dead, ReadErr: failed, ReadExit: 187}).kind; got != unreachable {
		t.Errorf("a read that failed on its own classified %s, want %s", got, unreachable)
	}
}
