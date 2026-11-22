package health

import (
	"fmt"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Fault is a verdict that ended a watch; recovery keys on its Kind.
type Fault struct {
	Kind Kind
	// Revise is a verdict the attempt may change for: one reached before the device held the URL, or a better link.
	Revise bool

	// Subject is what was watched (gate, output, or cast).
	Subject string

	// Vitals is the measurements the verdict was reached on.
	Vitals Vitals

	// Err is the producer's terminal error (so errors.Is still finds cancellation).
	Err error

	// Evidence is what the producer printed (retained even if castor killed it).
	Evidence []string
}

// DeviceStarved is the fault of a device that ended on its own while the read behind it still ran, having taken everything it had made.
func DeviceStarved(subject string, made media.Progress, handed int64) *Fault {
	return &Fault{
		Kind:    Starved,
		Subject: subject,
		// It took everything made, so it played everything made.
		Vitals: Vitals{Landed: made.Bytes, Position: made.Position, played: made.Position, handed: handed, playing: true},
	}
}

func (f *Fault) Error() string {
	if f.Err != nil {
		return fmt.Sprintf("%s: %s: %s", f.Subject, f.Kind, f.Err)
	}
	return fmt.Sprintf("%s: %s", f.Subject, f.Kind)
}

func (f *Fault) Unwrap() error { return f.Err }
