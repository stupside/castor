package cast

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/looplab/fsm"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/latest"
	"github.com/stupside/castor/services/mediaserver/internal/castlog"
	"github.com/stupside/castor/services/mediaserver/internal/lend"
	"github.com/stupside/castor/services/mediaserver/internal/mediaroute"
)

var (
	errStopped  = errors.New("cast stopped")
	errUndriven = errors.New("no device was lent to this cast")
)

// undriven is how long a cast waits for a device before it gives up.
const undriven = time.Minute

// cast is one cast: where it stands, its line to the device, what it serves, and who reads its lines.
type cast struct {
	ctx    context.Context
	id     string
	cancel context.CancelCauseFunc
	caster Caster
	source origin

	// machine moves only inside an update of now, so each move is taken and published as one.
	machine *fsm.FSM
	now     *latest.Value[view]
	done    chan struct{} // closes once the cast has ended

	line       *lend.Line
	deliveries *mediaroute.Deliveries
	logs       *castlog.Feed
}

// newCast awaits its device for undriven, and no longer than parent lasts; lending it one starts the cast.
func newCast(parent context.Context, id string, reach *url.URL, caster Caster, src *mediav1.Source) *cast {
	ctx, cancel := context.WithCancelCause(parent)
	c := &cast{
		id:         id,
		cancel:     cancel,
		caster:     caster,
		source:     originOf(src),
		done:       make(chan struct{}),
		line:       lend.NewLine(),
		deliveries: mediaroute.NewDeliveries(reach, id),
		logs:       castlog.NewFeed(),
	}
	// Everything the cast logs carries its feed, so its lines reach the watchers who asked for them.
	c.ctx = castlog.Into(ctx, c.logs)
	c.machine = c.lifecycle()
	c.now = latest.New(view{status: &castorv1.CastStatus{State: &castorv1.CastStatus_Measuring{Measuring: &castorv1.MeasuringStatus{}}}})
	time.AfterFunc(undriven, func() { c.end(eventAbandon, errUndriven) })
	context.AfterFunc(ctx, func() { c.end(eventAbandon, outcome(ctx, nil)) })
	return c
}

// view is a cast's status at one moment, and how it ended once it has.
type view struct {
	status *castorv1.CastStatus
	ended  *castorv1.Ended
}
