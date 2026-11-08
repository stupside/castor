package cast

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/castid"
	"github.com/stupside/castor/internal/latest"
	"github.com/stupside/castor/internal/machine"
	"google.golang.org/protobuf/proto"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/lend"
)

var (
	errStopped  = errors.New("cast stopped")
	errUndriven = errors.New("no device was lent to this cast")
	errEnded    = errors.New("the cast has ended")
)

// undriven is how long a cast waits for a device before it gives up.
const undriven = time.Minute

// cast is one cast: where it stands, its line to the device, and what it serves.
type cast struct {
	ctx    context.Context
	id     string
	cancel context.CancelCauseFunc
	caster Caster
	source origin
	start  time.Duration

	// machine moves only inside an update of now, so each move is taken and published as one.
	machine *machine.Machine[castState, castEvent]
	now     *latest.Value[view]
	done    chan struct{} // closes once the cast has ended

	line      *lend.Line
	device    atomic.Pointer[lend.Device]
	publisher output.Publisher
	// seeks reaches the attempt playing, and waits while none does.
	seeks chan recovery.Seek
}

// newCast awaits its device for undriven, and no longer than parent lasts; lending it one starts the cast.
func newCast(parent context.Context, id string, publisher output.Publisher, caster Caster, src *mediav1.Source, start time.Duration) *cast {
	ctx, cancel := context.WithCancelCause(castid.With(parent, id))
	c := &cast{
		id:        id,
		ctx:       ctx,
		cancel:    cancel,
		caster:    caster,
		start:     start,
		source:    originOf(src),
		done:      make(chan struct{}),
		line:      lend.NewLine(),
		publisher: publisher,
		seeks:     make(chan recovery.Seek),
	}
	c.machine = lifecycle()
	c.now = latest.New(view{status: &castorv1.CastStatus{State: &castorv1.CastStatus_Measuring{Measuring: &castorv1.MeasuringStatus{}}}})
	abandon := time.AfterFunc(undriven, func() { c.end(eventAbandon, errUndriven) })
	context.AfterFunc(ctx, func() {
		abandon.Stop()
		c.end(eventAbandon, outcome(ctx, nil))
	})
	return c
}

// view is a cast's status at one moment, and how it ended once it has.
type view struct {
	status *castorv1.CastStatus
	ended  *castorv1.Ended
}

// heard shows where the device plays while the cast is casting.
func (c *cast) heard(head *castorv1.Playhead) {
	c.now.Update(func(now view) (view, bool) {
		casting := now.status.GetCasting()
		if now.ended != nil || casting == nil || proto.Equal(casting.GetPlayhead(), head) {
			return now, false
		}
		next := view{status: proto.CloneOf(now.status)}
		next.status.GetCasting().Playhead = head
		return next, true
	})
}
