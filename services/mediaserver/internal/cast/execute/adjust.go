package execute

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/cast/splice"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// adjustable is a delivery whose rate and position change while the device plays it: the read's buffer as it is, or encoders over it.
type adjustable struct {
	s        *preparation
	spec     stageSpec
	dir      string
	stream   served
	audience health.Audience
	// reader is the cast's first read, which every read that takes its place is said through.
	reader *pull
	sought chan struct{}

	// changing serializes changes of rate and of position; mu guards what follows.
	changing sync.Mutex
	mu       sync.Mutex
	// stages is nil while the read's buffer is served as it is.
	stages *stages
	// cut is the byte of the served stream the encoders carry on after, zero for a stream they make from its start.
	cut     int64
	ceiling media.Bitrate
	made    int
	// run holds what the last seek started, released once another takes its place; nil before any seek.
	run *releases
}

var (
	_ switchable = (*adjustable)(nil)
	_ producer   = (*adjustable)(nil)
)

// served is the output an adjustable delivery is served through: carried on after a cut, or begun again from a seek.
type served interface {
	output.Continuer
	output.Seekable
}

func newAdjustable(s *preparation, spec stageSpec, dir string, stream served, audience health.Audience, reader *pull, chain *stages) *adjustable {
	a := &adjustable{s: s, spec: spec, dir: dir, stream: stream, audience: audience, reader: reader, sought: make(chan struct{}, 1), stages: chain, ceiling: s.MaxBitrate}
	s.releases.push(func() error {
		a.mu.Lock()
		run := a.run
		a.mu.Unlock()
		if run == nil {
			return nil
		}
		return run.release()
	})
	return a
}

// unit is what makes the delivery now: its encoders, else the read whose buffer is served.
func (a *adjustable) unit() producer {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stages != nil {
		return a.stages
	}
	return a.reader
}

// Done is closed once what makes the delivery now has ended; a change replaces it, so a watcher asks again.
func (a *adjustable) Done() <-chan struct{} { return a.unit().Done() }
func (a *adjustable) Err() error            { return a.unit().Err() }
func (a *adjustable) Evidence() []string    { return a.unit().Evidence() }

func (a *adjustable) chain() (*stages, int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stages, a.cut
}

// scope is where what the delivery starts now is released: with the last seek's, else with the attempt.
func (a *adjustable) scope() *releases {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.run != nil {
		return a.run
	}
	return a.s.releases
}

// Progress is what the read made until encoders carry it on, then the bytes of the stream as served and the media they hold.
func (a *adjustable) Progress() media.Progress {
	chain, cut := a.chain()
	if chain == nil {
		return a.reader.Progress()
	}
	made := chain.Progress()
	made.Bytes += cut
	return made
}

func (a *adjustable) Withheld() bool {
	chain, _ := a.chain()
	return chain != nil && chain.Withheld()
}

// taken is the bytes of the encoders' stream the device took.
func (a *adjustable) taken() int64 {
	handed, _ := a.audience.Handed()
	_, cut := a.chain()
	return max(0, handed-cut)
}

func (a *adjustable) Switch(ctx context.Context, ceiling media.Bitrate) (int64, error) {
	a.changing.Lock()
	defer a.changing.Unlock()
	var from int64
	var err error
	if chain, _ := a.chain(); chain != nil {
		from, err = chain.Switch(ctx, ceiling)
	} else {
		from, err = a.begin(ctx, ceiling)
	}
	if err == nil {
		a.mu.Lock()
		a.ceiling = ceiling
		a.mu.Unlock()
	}
	return from, err
}

// begin cuts the stream at the next keyframe past what the device was written, and carries it on from an encoder there.
func (a *adjustable) begin(ctx context.Context, ceiling media.Bitrate) (int64, error) {
	if err := a.workdir(); err != nil {
		return 0, err
	}
	read := a.reader.tail()
	handed := func() int64 { n, _ := a.audience.Handed(); return n }
	change, err := a.spec.begin(ctx, a.s.ctx, read, handed, ceiling)
	if err != nil {
		return 0, err
	}
	// A device that is written more while the encoder warms up is cut further on the next time.
	for attempt := range cutAttempts {
		cut, first, err := change.cut(ctx, handed()+a.spec.warm.margin(read, attempt))
		if err != nil {
			change.abandon()
			return 0, err
		}
		carried, err := a.spoolIn(a.scope(), change.opts.Format.Extension)
		if err != nil {
			first.close()
			change.abandon()
			return 0, err
		}
		if !a.stream.Continue(cut.Offset, carried) {
			// The device was written past the cut meanwhile.
			first.close()
			continue
		}
		a.mu.Lock()
		a.cut = cut.Offset
		a.mu.Unlock()
		chain := a.startIn(a.scope(), first, carried, cut.PTS)
		change.done()
		a.mu.Lock()
		a.stages = chain
		a.mu.Unlock()
		slog.InfoContext(ctx, "the stream carries on from an encoder", append([]any{"after_byte", cut.Offset, "at", cut.PTS.Round(time.Millisecond), "ceiling", ceiling.String()}, change.attrs()...)...)
		return cut.Offset, nil
	}
	change.abandon()
	return 0, errCutMissed
}

// Seek makes the stream again from at, a position in the attempt's program, for a device that asked for it there, and has the output serve it.
func (a *adjustable) Seek(ctx context.Context, at time.Duration) error {
	a.changing.Lock()
	defer a.changing.Unlock()
	// A device that gave up on a seek while a change held the delivery has asked again since.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.workdir(); err != nil {
		return err
	}
	from := a.reader.tail()
	program, known := a.s.attempt.Program, &from.probe
	if a.spec.ladder != nil {
		var err error
		if program, err = a.spec.ladder.program(ctx); err != nil {
			return err
		}
	}
	run := &releases{ctx: a.s.releases.ctx}
	a.mu.Lock()
	a.made++
	path := filepath.Join(a.dir, fmt.Sprintf("seek-%d%s", a.made, encoding.SpoolFormat.Extension))
	chain, ceiling := a.stages, a.ceiling
	a.mu.Unlock()
	read, err := a.s.readAt(ctx, run, program, a.s.attempt.Program.Start+at, from, path, known)
	if err != nil {
		_ = run.release()
		return err
	}
	waiting, cancel := context.WithTimeout(ctx, cutWait)
	first, err := read.index.Wait(waiting, 0)
	cancel()
	if err != nil {
		_ = run.release()
		return fmt.Errorf("the read made no keyframe: %w", err)
	}
	origin, _ := a.reader.index.First()
	began := max(0, first.PTS-origin)

	var out *storage.Spool
	var next *stages
	if chain != nil {
		if out, next, err = a.encodeFrom(ctx, run, read, first, ceiling); err != nil {
			_ = run.release()
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		_ = run.release()
		return err
	}
	// The output moves on before what it leaves stops, so no client takes that end for the stream's.
	if next == nil {
		a.stream.Restart(began, read.spool)
	} else {
		a.mu.Lock()
		a.stages, a.cut = next, 0
		a.mu.Unlock()
		a.stream.Restart(began, out)
		chain.stop()
	}
	from.seekWith(read)
	a.mu.Lock()
	left := a.run
	a.run = run
	a.mu.Unlock()
	if left != nil {
		go func() { _ = left.release() }()
	}
	select {
	case a.sought <- struct{}{}:
	default:
	}
	slog.InfoContext(ctx, "the device is served the stream from where it asked", "asked", at, "at", began.Round(time.Millisecond), "encoded", next != nil, "ceiling", ceiling.String())
	return nil
}

// encodeFrom starts encoders over read from first, as the plan for ceiling says, writing a stream of their own released with rel.
func (a *adjustable) encodeFrom(ctx context.Context, rel *releases, read *pull, first splice.Cut, ceiling media.Bitrate) (*storage.Spool, *stages, error) {
	opts, err := a.spec.plan(read, ceiling)
	if err != nil {
		return nil, nil, fmt.Errorf("planning the cast for %s: %w", ceiling, err)
	}
	st, err := cutStage(ctx, a.s.ctx, read, a.spec, opts, first, nil)
	if err != nil {
		return nil, nil, err
	}
	out, err := a.spoolIn(rel, opts.Format.Extension)
	if err != nil {
		st.close()
		return nil, nil, err
	}
	return out, a.startIn(rel, st, out, first.PTS), nil
}

func (a *adjustable) workdir() error {
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return fmt.Errorf("creating the delivery directory: %w", err)
	}
	return nil
}

// spoolIn opens a buffer for a stream the delivery makes anew, released with rel.
func (a *adjustable) spoolIn(rel *releases, extension string) (*storage.Spool, error) {
	a.mu.Lock()
	a.made++
	path := filepath.Join(a.dir, fmt.Sprintf("made-%d%s", a.made, extension))
	a.mu.Unlock()
	sp, err := storage.NewSpool(path)
	if err != nil {
		return nil, fmt.Errorf("opening the spool a stream is made in: %w", err)
	}
	rel.push(sp.Close)
	return sp, nil
}

// startIn runs encoders from first, cut at pts, written to out and released with rel; they count the media from where the cast's first read began.
func (a *adjustable) startIn(rel *releases, first *stage, out *storage.Spool, pts time.Duration) *stages {
	write := func(ctx context.Context, from io.Reader) error {
		_, err := io.Copy(out, from)
		out.CloseWrite(err)
		return err
	}
	origin, _ := a.reader.index.First()
	chain := startStages(a.s.ctx, a.reader, first, max(0, pts-origin), a.taken, a.spec, write)
	rel.worker(chain.stop, func() error {
		<-chain.Done()
		return chain.Err()
	})
	return chain
}
