package cast

import (
	"context"
	"google.golang.org/protobuf/types/known/emptypb"
	"time"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/latest"
)

// cast is one cast this server runs, and where it stands for its watchers.
type cast struct {
	id      string
	device  *castorv1.Device
	source  *castorv1.Source
	started time.Time
	// stop ends the cast; its cause is why, errStopped when asked.
	stop context.CancelCauseFunc
	now  *latest.Value[view]
}

// view is a cast at one moment.
type view struct {
	status *castorv1.CastStatus
	// media is the media server's cast, once started.
	media string
	ended *castorv1.Ended
}

func newCast(id string, device *castorv1.Device, source *castorv1.Source, stop context.CancelCauseFunc) *cast {
	return &cast{id: id, device: device, source: source, started: time.Now(), stop: stop, now: latest.New(view{status: &castorv1.CastStatus{State: &castorv1.CastStatus_Connecting{Connecting: &emptypb.Empty{}}}})}
}

// update publishes what change makes of the cast, unless it has ended.
func (c *cast) update(change func(*view)) {
	c.now.Update(func(v view) (view, bool) {
		if v.ended != nil {
			return v, false
		}
		change(&v)
		return v, true
	})
}

// show is the media server's status, as the cast's.
func (c *cast) show(status *castorv1.CastStatus) {
	c.update(func(v *view) { v.status = status })
}
