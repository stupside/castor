package execute

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// feed is what a served cast's encoder reads: the read's buffer where there is one, else the program's source.
type feed struct {
	buffered *buffered
	program  media.Program
}

// spliced is whether the plan joins a seamed program itself; a buffer was made one stream by the floor.
func (f feed) spliced() bool { return f.buffered == nil && f.program.Seamed() }

// delivery is what serves a cast: the mechanism the device fetches from, and what writes the bytes it serves.
type delivery struct {
	sink   output.Output
	output producer
	pub    output.Publication
}

// producer is what writes the served bytes, judged by the media it has made rather than bytes a muxer pads.
type producer interface {
	health.Producer
	health.Telemetry
}

// validateOutput checks the encoder's actual result, not just its requested flags.
// Probing local artifacts never consumes the receiver's HTTP delivery counters.
func (s *preparation) validateOutput(d delivery, opts encodePlan, f feed) error {
	path := d.sink.Artifact().Path
	if path == "" {
		return fmt.Errorf("encoded delivery has no local artifact to validate")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	expected := opts.Probe.RequiredTracks().Or(s.attempt.Program.RequiredTracks())
	if f.buffered != nil {
		expected = expected.Or(f.buffered.expected)
	}
	return encoding.ValidateOutput(ctx, s.Probes.File(path), opts.delivery.Capabilities, opts.Format.ContentType, expected, d.output.Done())
}

func (s *preparation) produce(work string, f feed, opts encodePlan, burn burnRenderer) (delivery, error) {
	o := output.Opening{
		Live:          f.buffered != nil && f.buffered.live || slices.ContainsFunc(f.program.Inputs, func(in media.Input) bool { return in.Fetching().Live }),
		Format:        opts.Format,
		Headers:       s.capabilities.ServedHeaders,
		IdleGrace:     idleGrace,
		WriteDeadline: writeDeadline,
	}
	// A burn-in follows the encoder's progress, so only a cast without one can do without an encoder.
	if f.buffered != nil && burn == nil && opts.Verbatim() {
		if relay, ok := opts.delivery.Plan.(output.Relayer); ok {
			return s.relay(relay, o, f.buffered.reader)
		}
	}

	dir := filepath.Join(work, "delivery")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return delivery{}, fmt.Errorf("creating the delivery directory: %w", err)
	}

	reservation, err := opts.delivery.Plan.Reserve(dir)
	if err != nil {
		return delivery{}, err
	}
	if reservation != nil {
		s.releases.push(reservation)
	}
	encoderOpts := opts.EncodeOptions
	encoderOpts.Format = opts.delivery.Plan.EncoderFormat()
	var step func(media.Progress)
	if burn != nil {
		step = burn.Writer(s.ctx)
	}
	prepared, err := opts.delivery.Plan.Prepare(o, dir)
	if err != nil {
		return delivery{}, err
	}
	s.releases.push(prepared.Output.Close)
	pub, err := s.Publisher.Publish(s.ctx, prepared.Output.Handler())
	if err != nil {
		return delivery{}, err
	}
	// Requests must stop before the storage they borrow is released.
	s.releases.publication(pub.Close)
	var tail io.ReadCloser
	if f.buffered != nil {
		var err error
		if tail, err = f.buffered.reader.spool.TailAt(s.ctx, 0); err != nil {
			return delivery{}, err
		}
	}

	proc, err := s.startEncoder(encoderOpts, tail, step, dir)
	if err != nil {
		if tail != nil {
			_ = tail.Close()
		}
		return delivery{}, err
	}
	production := startProduction(s.ctx, proc, tail, prepared.Write)
	s.releases.worker(production.stop, func() error {
		<-production.Done()
		return production.Err()
	})
	if prepared.WatchStorage != nil {
		s.watchStorage(prepared.WatchStorage)
	}
	return delivery{sink: prepared.Output, output: production, pub: pub}, nil
}

// relay borrows the read's spool; its writer remains owned by that read.
func (s *preparation) relay(relay output.Relayer, o output.Opening, reader *pull) (delivery, error) {
	sink := relay.ServeSpool(o, reader.spool)
	s.releases.push(sink.Close)
	pub, err := s.Publisher.Publish(s.ctx, sink.Handler())
	if err != nil {
		return delivery{}, err
	}
	s.releases.publication(pub.Close)
	slog.InfoContext(s.ctx, "serving the read's buffer without another encoder")
	return delivery{sink: sink, output: reader, pub: pub}, nil
}

func (d delivery) URL() *url.URL { return d.pub.URL(d.sink.Path()) }

// Storage safety lasts until the attempt releases the published artifacts,
// including the interval after production has naturally completed.
func (s *preparation) watchStorage(watch func(context.Context) error) {
	done := make(chan struct{})
	var result error
	go func() {
		defer close(done)
		result = watch(s.ctx)
		if result != nil {
			select {
			case s.failures <- result:
			case <-s.ctx.Done():
			}
		}
	}()
	s.releases.worker(nil, func() error { <-done; return result })
}

// starved reports whether the device ended on an unfinished read after taking everything it had made, and what that was.
func starved(f feed, d delivery) (made media.Progress, sent int64, ok bool) {
	if f.buffered == nil {
		return made, 0, false
	}
	select {
	case <-f.buffered.reader.Done():
		return made, 0, false
	default:
	}
	audience := d.sink.Audience()
	if audience == nil {
		return made, 0, false
	}
	sent, _ = audience.Handed()
	made = d.output.Progress()
	return made, sent, made.Bytes > 0 && sent >= made.Bytes
}

const (
	// idleGrace is how long a device that stopped asking is waited for before the delivery is done.
	idleGrace = 30 * time.Second

	// writeDeadline outlasts a stall verdict, so the watch judges a quiet device before a write gives up.
	writeDeadline = health.StallWindow + idleGrace
)
