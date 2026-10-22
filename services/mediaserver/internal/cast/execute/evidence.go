package execute

import (
	"errors"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// evidence is what an attempt left behind, read from how far it ran and how it ended.
func evidence(r ran, err error, cancelled bool) recovery.Evidence {
	e := recovery.Evidence{Reached: r.reached, Cancelled: cancelled}
	if err == nil {
		e.Reached = health.Delivered
	}
	// A read cast reaches its delivery only once the playback gate proved the buffer.
	e.Buffered = r.reader != nil && r.reached >= health.Opening

	if p := r.reader; p != nil {
		e.ReadErr = p.Err()
		e.ReadExit = p.exitStatus()
		e.ReadIncomplete = p.lostMedia()
		e.Copied = p.copying()
		e.Lines = p.Evidence()
	}

	if refused, ok := errors.AsType[*playRefused](err); ok {
		e.PlayErr, e.Handoff = refused.err, refused.source
	}
	if unread, ok := errors.AsType[*timelineUnreadable](err); ok {
		e.TimelineErr = unread.err
	}
	if undelivered, ok := errors.AsType[*health.Undelivered](err); ok {
		e.Undelivered = undelivered
	}
	// A device its family saw go away while the cast played (reported by the playback observer).
	if gone, ok := errors.AsType[*media.Gone](err); ok {
		e.DeviceGone = gone
	}
	if verdict, ok := errors.AsType[*health.Fault](err); ok {
		e.Verdict, e.Vitals = verdict.Kind, verdict.Vitals
		if len(e.Lines) == 0 {
			e.Lines = verdict.Evidence
		}
	}
	if errors.Is(err, storage.ErrSpoolExpired) {
		e.ResourceErr = storage.ErrSpoolExpired
	}
	if errors.Is(err, storage.ErrSpoolLimit) || errors.Is(e.ReadErr, storage.ErrSpoolLimit) {
		e.ResourceErr = storage.ErrSpoolLimit
	}
	return e
}
