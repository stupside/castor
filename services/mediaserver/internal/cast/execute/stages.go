package execute

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/splice"
	"github.com/stupside/castor/services/mediaserver/internal/cast/teardown"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

const (
	// cutMargin is the least a cut is taken past what an encoder has read, so the encoder can still be stopped there.
	cutMargin = 256 << 10

	// cutWait is how long a change waits for the read to reach a keyframe past the encoder.
	cutWait = 30 * time.Second

	// warmup is how long a new encoder has to make its first byte before the old one is cut for it.
	warmup = 10 * time.Second

	// tsPacket is the unit an MPEG-TS stream is read in, which output carried on after a failed encoder is aligned to.
	tsPacket = 188
)

// errCutMissed is an encoder that read past every cut offered before it could be stopped at one.
var errCutMissed = errors.New("the encoder read past the cut before it could be stopped")

// stage is one encoder reading the read's buffer from a place in it.
type stage struct {
	proc *ffmpeg.Process
	in   *splice.Cursor
	// src is the read whose buffer it reads, and opts how it makes what it passes.
	src  *pull
	opts encoding.EncodeOptions
	out  *bufio.Reader
	// passed is the bytes of its output the delivery took.
	passed int64
	// from is where its picture begins on the read's clock, zero for the read's start.
	from time.Duration

	// scan reads what the delivery took of its output, for the last picture and sound that opened in it.
	scan              splice.Scanner
	picture, sound    time.Duration
	pictured, sounded bool
	// made is a stage the delivery took output from, which only then can be told hung.
	made atomic.Bool
	// hung is an encoder the watch stopped for making nothing while its input waited, after waited.
	hung   atomic.Bool
	waited atomic.Int64
	// replaced is a failed stage another was cut in for, whose failure is not the delivery's.
	replaced bool

	reaped sync.Once
	err    error
}

func newStage(proc *ffmpeg.Process, in *splice.Cursor, src *pull, opts encoding.EncodeOptions) *stage {
	return &stage{proc: proc, in: in, src: src, opts: opts, out: bufio.NewReaderSize(proc.Stdout, 64<<10), scan: spoolFraming().Scanner()}
}

// saw notes what of the stage's output the delivery took.
func (f *stage) saw(p []byte) {
	f.scan.Scan(p, func(e splice.Event) {
		switch e.Kind {
		case splice.Sound:
			f.sound, f.sounded = max(f.sound, e.PTS), true
		default:
			f.picture, f.pictured = max(f.picture, e.PTS), true
		}
	})
}

// result is how the stage's encoder ended, once it has.
func (f *stage) result(ctx context.Context) error {
	f.reaped.Do(func() { f.err = encoderResult(ctx, f.proc) })
	return f.err
}

// hardware is the hardware video encoder the stage runs, empty for none.
func (f *stage) hardware() string {
	if venc, ok := f.opts.Video.Encode(); ok && venc.Encoder.Hardware {
		return venc.Encoder.Name
	}
	return ""
}

func (f *stage) kill() {
	f.proc.Kill()
	_ = f.in.Close()
	_ = f.proc.Stdout.Close()
}

// close kills an encoder no chain will reap, and reaps it.
func (f *stage) close() {
	f.kill()
	_ = f.proc.Wait()
}

// stageSpec is how a delivery makes its stages: what each is to do over a read for a ceiling on the rate, and how it is started on an input.
type stageSpec struct {
	plan  func(src *pull, ceiling media.Bitrate) (encoding.EncodeOptions, error)
	probe func(src *pull) media.ProbeInfo
	spawn func(opts encoding.EncodeOptions, in io.Reader) (*ffmpeg.Process, error)
	// ladder moves the read between the source's rungs, nil where it has one.
	ladder *ladder
	warm   *warmth
	// soft plans what plan would in software, for a hardware encoder that fails; nil where the host has no software encoder.
	soft func(src *pull, ceiling media.Bitrate) (encoding.EncodeOptions, error)
	// retire stops offering a hardware encoder software replaced, nil where nothing does.
	retire func(name string)
}

// stages is a chain of encoders over one buffer, each cut in where the last stopped, served as one stream no more than lead ahead of the device.
type stages struct {
	ctx    context.Context
	reader *pull
	spec   stageSpec
	// from is where in the attempt's program the first encoder begins, which the chain's weight is counted from.
	from   time.Duration
	handed func() int64
	done   chan struct{}
	stop   func()
	err    error

	// switching serializes changes; mu guards the chain.
	switching sync.Mutex
	mu        sync.Mutex
	cur, next *stage
	// nextAt is the byte of the read the encoder waiting to take over starts at.
	nextAt  int64
	retired []*stage
	// base is the bytes the retired stages made.
	base int64
	// pad is what aligns the stream after an encoder that failed mid-packet, passed before the next encoder's output.
	pad []byte
	// ceiling is the rate the chain was last made for, which a software stage carrying on is made for too.
	ceiling atomic.Int64
	// waiting times the read of the current encoder's output.
	waiting waitClock

	// taken is the bytes the device took, sampled every takeEvery, for the lead it is given.
	takesMu sync.Mutex
	taken   media.Trail
	sampled time.Time

	passed   atomic.Int64
	withheld atomic.Bool
	origin   atomic.Int64
	origined atomic.Bool
}

// startStages starts the chain at first, from where in the program it begins, writing what it makes through write.
func startStages(parent context.Context, reader *pull, first *stage, from time.Duration, handed func() int64, spec stageSpec, write func(context.Context, io.Reader) error) *stages {
	ctx, cancel := context.WithCancel(parent)
	f := &stages{ctx: ctx, reader: reader, spec: spec, from: from, handed: handed, cur: first, done: make(chan struct{}), taken: media.Trail{Span: takeSpan}}
	f.stop = func() { cancel(); f.killAll() }
	stopped := context.AfterFunc(ctx, f.killAll)
	go f.watch(ctx)
	go func() {
		defer close(f.done)
		defer cancel()
		defer stopped()
		packaging := write(ctx, f)
		if packaging != nil {
			f.killAll()
		}
		f.release()
		f.err = errors.Join(teardown.Failures(ctx, packaging), f.results())
	}()
	return f
}

func (f *stages) all() []*stage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.DeleteFunc(append(slices.Clone(f.retired), f.cur, f.next), func(st *stage) bool { return st == nil })
}

func (f *stages) killAll() {
	for _, each := range f.all() {
		each.kill()
	}
}

func (f *stages) release() {
	for _, each := range f.all() {
		_ = each.in.Close()
		_ = each.proc.Stdout.Close()
	}
}

func (f *stages) results() error {
	var err error
	for _, each := range f.all() {
		switch {
		case each.hung.Load():
			// A hung encoder may sit in a driver call that holds off even a kill, so it is reaped without waiting on it.
			go each.result(f.ctx)
		case each.replaced:
			_ = each.result(f.ctx)
		default:
			err = errors.Join(err, each.result(f.ctx))
		}
	}
	return err
}

// Read is the stream the delivery writes: each encoder's output in turn, held back while the device is the lead behind.
func (f *stages) Read(p []byte) (int, error) {
	if err := f.gate(); err != nil {
		return 0, err
	}
	for {
		f.mu.Lock()
		cur := f.cur
		if len(f.pad) > 0 {
			n := copy(p, f.pad)
			f.pad = f.pad[n:]
			f.mu.Unlock()
			f.passed.Add(int64(n))
			return n, nil
		}
		f.mu.Unlock()
		f.waiting.start()
		n, err := cur.out.Read(p)
		f.waiting.stop()
		if n > 0 {
			cur.passed += int64(n)
			cur.saw(p[:n])
			cur.made.Store(true)
			f.passed.Add(int64(n))
			return n, nil
		}
		if errors.Is(err, io.EOF) && f.advance() {
			continue
		}
		var failure error
		if cur.hung.Load() {
			failure = &encoderHung{waited: time.Duration(cur.waited.Load())}
		} else {
			failure = cur.result(f.ctx)
		}
		if failure != nil && f.carryOn(cur, failure) {
			continue
		}
		if cur.hung.Load() {
			return 0, failure
		}
		return 0, err
	}
}

// watch stops the current encoder once it has made nothing for chainHangWindow while its input waited, so the chain can carry on without it.
func (f *stages) watch(ctx context.Context) {
	tick := time.NewTicker(hangPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		cur := f.current()
		if !cur.made.Load() || cur.hung.Load() {
			continue
		}
		if waited := silent(f.waiting.waited(), cur.in.Unread(cur.src.spool.Size())); waited >= chainHangWindow {
			cur.waited.Store(int64(waited))
			cur.hung.Store(true)
			slog.WarnContext(ctx, "the encoder made nothing while its input waited, so it is stopped", "encoder", cur.opts.Video.Name(), "waited", waited.Round(time.Second))
			cur.kill()
		}
	}
}

// carryOn cuts a software encoder in where a failed hardware one left the device, so the device goes on with one stream; false where it cannot.
func (f *stages) carryOn(failed *stage, failure error) bool {
	name := failed.hardware()
	if name == "" || f.spec.soft == nil {
		return false
	}
	f.switching.Lock()
	defer f.switching.Unlock()
	f.mu.Lock()
	pending := f.next
	f.next = nil
	f.mu.Unlock()
	if pending != nil {
		// Planned with the encoder that failed, and cut in further on than the device was left.
		f.discard(pending)
	}
	// The picture the device was handed last may have been cut short, so it is made again.
	from := failed.from
	if failed.pictured {
		from = failed.picture
	}
	cut, ok := failed.src.index.Before(from)
	if !ok {
		slog.WarnContext(f.ctx, "no keyframe to carry the delivery on in software from", "encoder", name, "error", failure)
		return false
	}
	opts, err := f.spec.soft(failed.src, media.Bitrate(f.ceiling.Load()))
	if err != nil {
		slog.WarnContext(f.ctx, "the delivery cannot be planned in software", "encoder", name, "error", errors.Join(failure, err))
		return false
	}
	if venc, ok := opts.Video.Encode(); ok && from > cut.PTS {
		venc.From = from
		opts.Video = codec.Encode(venc)
	}
	var skip splice.Trimmer
	if failed.sounded {
		skip = spoolFraming().Trimmer(cut.Tables, failed.sound+time.Nanosecond)
	}
	next, err := cutStage(f.ctx, f.ctx, failed.src, f.spec, opts, cut, skip)
	if err != nil {
		slog.WarnContext(f.ctx, "software could not carry the delivery on", "encoder", name, "error", errors.Join(failure, err))
		return false
	}
	next.from = from
	pad := (tsPacket - failed.passed%tsPacket) % tsPacket
	f.mu.Lock()
	failed.replaced = true
	f.retired = append(f.retired, failed)
	f.base += failed.passed + pad
	f.pad = bytes.Repeat([]byte{0xff}, int(pad))
	f.cur = next
	f.mu.Unlock()
	_ = failed.in.Close()
	if f.spec.retire != nil {
		f.spec.retire(name)
	}
	slog.WarnContext(f.ctx, "the hardware encoder failed, so the delivery carries on in software", "encoder", name, "from", from.Round(time.Millisecond), "error", failure)
	return true
}

// advance takes up the encoder that was cut in for the one that ended, if there is one.
func (f *stages) advance() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.next == nil {
		return false
	}
	f.retired = append(f.retired, f.cur)
	f.base += f.cur.passed
	_ = f.cur.in.Close()
	f.cur, f.next = f.next, nil
	return true
}

// Switch cuts the delivery over to the plan for ceiling at the next keyframe the encoder has not read past; a newer ask replaces a cut still waiting.
func (f *stages) Switch(ctx context.Context, ceiling media.Bitrate) (int64, error) {
	f.switching.Lock()
	defer f.switching.Unlock()
	cur := f.current()
	change, err := f.spec.begin(ctx, f.ctx, cur.src, cur.in.Offset, ceiling)
	if err != nil {
		return 0, err
	}
	if !change.moved() && change.makes(cur.opts) {
		// What the stage makes already is what is asked: a cut would change nothing the device sees.
		return 0, nil
	}
	// An encoder that runs ahead while the new one warms up is cut further on the next time.
	for attempt := range cutAttempts {
		cur = f.current()
		cut, next, err := change.cut(ctx, cur.in.Offset()+f.spec.warm.margin(cur.src, attempt))
		if err != nil {
			change.abandon()
			return 0, err
		}
		superseded, stopped := f.cutIn(cur, cut.Offset, next)
		if stopped {
			if superseded != nil {
				f.discard(superseded)
			}
			change.done()
			f.ceiling.Store(int64(ceiling))
			slog.InfoContext(ctx, "the delivery is cut to a new encoder", append([]any{"at", cut.PTS.Round(time.Millisecond), "offset", cut.Offset, "ceiling", ceiling.String()}, change.attrs()...)...)
			return 0, nil
		}
		f.discard(next)
	}
	change.abandon()
	return 0, errCutMissed
}

// discard stops an encoder that will not take over, and leaves it to be reaped with the chain.
func (f *stages) discard(st *stage) {
	st.kill()
	f.mu.Lock()
	f.retired = append(f.retired, st)
	f.mu.Unlock()
}

func (f *stages) current() *stage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cur
}

// cutIn has next take over from cur at byte at, refusing where cur moved on, read past at, or an earlier cut waits.
func (f *stages) cutIn(cur *stage, at int64, next *stage) (superseded *stage, stopped bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cur != cur || f.next != nil && at > f.nextAt || !cur.in.Stop(at) {
		return nil, false
	}
	superseded, f.next, f.nextAt = f.next, next, at
	return superseded, true
}

// cutStage starts an encoder on src's buffer from cut, less what skip drops, and waits for its first byte; life ends it, ctx the wait.
func cutStage(ctx, life context.Context, src *pull, spec stageSpec, opts encoding.EncodeOptions, cut splice.Cut, skip splice.Trimmer) (*stage, error) {
	in, err := splice.Open(life, src.spool, cut.Offset, cut.Tables, skip)
	if err != nil {
		return nil, fmt.Errorf("reading the buffer from the cut: %w", err)
	}
	proc, err := spec.spawn(opts, in)
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	next := newStage(proc, in, src, opts)
	began := time.Now()
	made := make(chan error, 1)
	go func() { _, err := next.out.Peek(1); made <- err }()
	select {
	case err := <-made:
		if err != nil {
			next.kill()
			return nil, fmt.Errorf("the encoder made nothing at the cut: %w", errors.Join(err, encoderResult(life, proc)))
		}
		spec.warm.note(time.Since(began))
		return next, nil
	case <-time.After(warmup):
		next.close()
		return nil, fmt.Errorf("the encoder made nothing at the cut within %s", warmup)
	case <-ctx.Done():
		next.close()
		return nil, context.Cause(ctx)
	}
}

// setOrigin is where the read's picture begins on its clock, which the delivery's media is counted from.
func (f *stages) setOrigin() time.Duration {
	if f.origined.Load() {
		return time.Duration(f.origin.Load())
	}
	if first, ok := f.reader.index.First(); ok {
		f.origin.Store(int64(first))
		f.origined.Store(true)
		return first
	}
	return 0
}

// Progress is what the chain has made: the bytes of every encoder, and the media since the read's picture began.
func (f *stages) Progress() media.Progress {
	f.mu.Lock()
	cur, base := f.cur, f.base
	f.mu.Unlock()
	made := cur.proc.Progress()
	// ffmpeg's out_time does not follow the media under -copyts, so the media is where the encoder has read the buffer to.
	at, _ := cur.src.index.At(cur.in.Offset())
	return media.Progress{Position: max(0, at-f.setOrigin()), Bytes: base + made.Bytes, Speed: made.Speed}
}

func (f *stages) Done() <-chan struct{} { return f.done }

func (f *stages) Err() error {
	select {
	case <-f.done:
		return f.err
	default:
		return nil
	}
}

func (f *stages) Evidence() []string {
	f.mu.Lock()
	cur := f.cur
	f.mu.Unlock()
	return cur.proc.Evidence().Lines
}

func (f *stages) Close() error { f.stop(); <-f.done; return f.err }
