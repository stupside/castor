package recovery

import (
	"errors"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Interrupted records why an attempt was canceled separately from failures
// observed while joining its work and releasing its resources.
type Interrupted struct {
	Cause   error
	Failure error
}

func (e *Interrupted) Error() string { return errors.Join(e.Cause, e.Failure).Error() }
func (e *Interrupted) Unwrap() []error {
	if e.Failure == nil {
		return []error{e.Cause}
	}
	return []error{e.Cause, e.Failure}
}

// Outcome is what one attempt did: whether it worked, and the material to say why not.
type Outcome struct {
	Err error

	// Evidence is what the attempt left behind: the only material a classification rule reads.
	Evidence Evidence
}

// attribute decides whose account of a failure the cast reports: a verdict is left alone.
func attribute(err, readErr error) error {
	_, judged := errors.AsType[*health.Fault](err)
	if _, interrupted := errors.AsType[*Interrupted](err); interrupted {
		return err
	}
	switch {
	case err == nil || readErr == nil || judged:
		return err
	case errors.Is(err, readErr):
		// The existing chain may also contain independent cleanup failures.
		return err
	default:
		return errors.Join(readErr, err)
	}
}

type Evidence struct {
	// Reached is how far the attempt got, which decides whether the loop may still change its mind.
	Reached health.Phase
	// Began is where in the program the attempt's playback began, a little before its start for a device handed whole segments.
	Began time.Duration

	Cancelled bool

	// ResourceErr is local budget exhaustion; switching source or encoding cannot fix it.
	ResourceErr error

	Verdict health.Kind
	Vitals  health.Vitals

	ReadErr error

	// ReadExit is the exit status that error came with.
	ReadExit int

	// ReadIncomplete is a read that ended short of what the source declared, whatever its exit status.
	ReadIncomplete bool

	Copied media.Axes

	// Buffered is a delivery that read castor's own buffer, which the playback gate had already proven.
	Buffered bool

	// PlayErr is the device's own refusal of the URL it was handed.
	PlayErr error

	// Handoff is a device pointed at the source itself rather than at what castor serves.
	Handoff bool

	// TimelineErr is a source whose timeline castor must keep and could not read when the read was set up.
	TimelineErr error

	Undelivered error

	// DeviceGone is what an unplugged, crashed or switched-off device looks like from here.
	DeviceGone *media.Gone

	// Lines is the retained stderr of the party being blamed. No rule DECIDES on it: prose is not a contract.
	Lines []string
}
