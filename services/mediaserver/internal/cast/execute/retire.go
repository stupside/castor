package execute

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"sync/atomic"
	"syscall"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Retirement keeps hardware video encoding out of later casts once a hardware encode crashed or hung.
//
// A cast whose hardware encode failed is made again in software, which suits a picture one encoder refuses, such as a
// small one: the next cast's picture may suit it. An encoder that crashed or hung says nothing of the picture, and on a
// host where it does so it would fail every cast that way first.
type Retirement struct{ retired atomic.Bool }

// Encoders is lookup, asked for software once hardware was retired.
func (r *Retirement) Encoders(lookup codec.Encoders) codec.Encoders {
	return func(ctx context.Context, c media.Codec, software bool) (ffmpeg.Encoder, bool) {
		return lookup(ctx, c, software || r.retired.Load())
	}
}

// Runner is run, retiring hardware once an attempt's hardware encode crashed or hung.
func (r *Retirement) Runner(run recovery.Runner) recovery.Runner { return retiring{run: run, r: r} }

type retiring struct {
	run recovery.Runner
	r   *Retirement
}

func (t retiring) Run(ctx context.Context, a recovery.Attempt) recovery.Outcome {
	o := t.run.Run(ctx, a)
	e := o.Evidence
	if e.Hardware && e.EncodeErr != nil && (e.Verdict == health.Wedged || crashed(e.EncodeErr)) {
		t.r.Retire(ctx, e.EncodeErr)
	}
	return o
}

// Retire keeps hardware video out of later casts.
func (r *Retirement) Retire(ctx context.Context, why error) {
	if !r.retired.Swap(true) {
		slog.WarnContext(ctx, "hardware video encoding retired until castor restarts", "error", why)
	}
}

// crashed is an ffmpeg ended by a signal; castor's own kills never reach an encode's failure.
func crashed(err error) bool {
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		return false
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	return ok && status.Signaled()
}
