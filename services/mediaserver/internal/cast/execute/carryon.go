package execute

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/splice"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// A hardware encoder that fails in a chain ends the attempt, and the cast is made again in software, which hands a
// DLNA TV a new stream to reload. Where the host carries chains on, a software encoder is cut in instead at the last
// keyframe before the last picture the device was handed, makes only the pictures after it and the sound after the
// last sound handed, and its output follows the failed one's in the same MPEG-TS stream, aligned to the next packet.
// Only a chain writing MPEG-TS is carried on, since only that stream is scanned for what the device was handed.

// tsPacket is the unit an MPEG-TS stream is read in, which the output carried on after a failed encoder is aligned to.
const tsPacket = 188

// hangPoll is how often a chain's encoder is looked at for silence.
const hangPoll = 250 * time.Millisecond

// chainHangWindow is how long a chain's hardware encoder may make nothing while castor waits on it with input left
// before it is stopped and carried on; under health.Warmup, so the chain carries on before the cast is judged wedged.
var chainHangWindow = 5 * time.Second

// errChainHung is a chain's hardware encoder stopped for making nothing while its input waited.
var errChainHung = errors.New("the hardware encoder made nothing while its input waited")

// carryState is what of a stage's output the delivery took, and how the stage ended.
type carryState struct {
	// scan reads the stage's output for what opens in it; nil where the chain does not write MPEG-TS.
	scan splice.Scanner
	// from is where the stage's picture begins on the read's clock.
	from              time.Duration
	picture, sound    time.Duration
	pictured, sounded bool

	// took is a stage the delivery took output from, which only then can be told hung.
	took atomic.Bool
	hung atomic.Bool
	// replaced is a failed stage another was cut in for, whose failure is not the delivery's.
	replaced bool

	reaped sync.Once
	err    error
}

func newCarryState(opts encoding.EncodeOptions) *carryState {
	c := &carryState{}
	if opts.Format.ContentType == media.MPEGTS {
		c.scan = spoolFraming().Scanner()
	}
	return c
}

// saw notes what of the stage's output the delivery took.
func (c *carryState) saw(p []byte) {
	c.took.Store(true)
	if c.scan == nil {
		return
	}
	c.scan.Scan(p, func(e splice.Event) {
		if e.Kind == splice.Sound {
			c.sound, c.sounded = max(c.sound, e.PTS), true
			return
		}
		c.picture, c.pictured = max(c.picture, e.PTS), true
	})
}

// reap is how the stage's encoder ended, waited for once.
func (st *stage) reap(ctx context.Context) error {
	st.carry.reaped.Do(func() { st.carry.err = encoderResult(ctx, st.proc, onHardware(st.opts.Video), true) })
	return st.carry.err
}

// result is how the stage ended for the delivery: a stage carried on, or stopped hung, leaves no failure of its own.
func (st *stage) result(ctx context.Context) error {
	if st.carry.hung.Load() {
		// A hung encoder may sit in a driver call that holds off even a kill, so it is reaped without waiting on it.
		go func() { _ = st.reap(ctx) }()
		return nil
	}
	err := st.reap(ctx)
	if st.carry.replaced {
		return nil
	}
	return err
}

// carryable is a stage a failed encoder can be carried on from: a hardware picture in a scanned stream, with software to plan.
func (f *stages) carryable(st *stage) bool {
	return st.carry.scan != nil && onHardware(st.opts.Video) && f.spec.soft != nil
}

// carryOn cuts a software encoder in where the failed hardware one left the device, so the device goes on with one
// stream; failure is a hung stage's own, for a delivery that could not carry it on.
func (f *stages) carryOn(failed *stage) (bool, error) {
	hung := failed.carry.hung.Load()
	var failure error
	if hung {
		failure = &encoderFailed{err: errChainHung, hardware: true, lines: failed.proc.Evidence().Lines}
	}
	if !f.carryable(failed) {
		return false, failure
	}
	if !hung {
		failure = failed.reap(f.ctx)
		if _, own := errors.AsType[*encoderFailed](failure); !own {
			return false, nil
		}
	}
	f.switching.Lock()
	defer f.switching.Unlock()
	name := failed.opts.Video.Name()
	// The picture the device was handed last may have been cut short, so it is made again.
	from := failed.carry.from
	if failed.carry.pictured {
		from = failed.carry.picture
	}
	cut, ok := failed.src.index.Before(from)
	if !ok {
		cut, ok = failed.src.index.After(0)
	}
	if !ok {
		slog.WarnContext(f.ctx, "no keyframe to carry the delivery on in software from", "encoder", name, "error", failure)
		return false, failure
	}
	opts, err := f.spec.soft(failed.src, media.Bitrate(f.ceiling.Load()))
	if err != nil {
		slog.WarnContext(f.ctx, "the delivery cannot be planned in software", "encoder", name, "error", errors.Join(failure, err))
		return false, failure
	}
	if venc, encoded := opts.Video.Encode(); encoded && from > cut.PTS {
		venc.From = from
		opts.Video = codec.Encode(venc)
	}
	var skip splice.Trimmer
	if failed.carry.sounded {
		skip = spoolFraming().Trimmer(cut.Tables, failed.carry.sound+time.Nanosecond)
	}
	next, err := cutStage(f.ctx, f.ctx, failed.src, f.spec, opts, cut, skip)
	if err != nil {
		slog.WarnContext(f.ctx, "software could not carry the delivery on", "encoder", name, "error", errors.Join(failure, err))
		return false, failure
	}
	next.carry.from = from
	pad := (tsPacket - failed.passed%tsPacket) % tsPacket
	f.mu.Lock()
	superseded := f.next
	f.next = next
	failed.carry.replaced = true
	// What the failed encoder left of its last packet is filled out, then the software encoder takes over.
	failed.out = bufio.NewReader(bytes.NewReader(bytes.Repeat([]byte{0xff}, int(pad))))
	f.mu.Unlock()
	if superseded != nil {
		// Planned with the encoder that failed.
		f.discard(superseded)
	}
	slog.WarnContext(f.ctx, "the hardware encoder failed, so the delivery carries on in software", "encoder", name, "from", from.Round(time.Millisecond), "error", failure)
	return true, nil
}

// watchHang stops the chain's hardware encoder once it has made nothing for chainHangWindow while castor waited on it
// with input left, so the chain carries it on.
func (f *stages) watchHang(ctx context.Context) {
	if f.spec.soft == nil {
		return
	}
	tick := time.NewTicker(hangPoll)
	defer tick.Stop()
	var watched *stage
	var quiet time.Duration
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		cur := f.current()
		if cur != watched {
			watched, quiet = cur, 0
		}
		if !f.carryable(cur) || !cur.carry.took.Load() || cur.carry.hung.Load() || !cur.made.waiting() || !cur.in.Behind() {
			quiet = 0
			continue
		}
		if quiet += hangPoll; quiet >= chainHangWindow {
			cur.carry.hung.Store(true)
			slog.WarnContext(ctx, "the hardware encoder made nothing while its input waited, so it is stopped", "encoder", cur.opts.Video.Name(), "waited", quiet)
			cur.kill()
		}
	}
}
