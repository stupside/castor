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
	"github.com/stupside/castor/services/mediaserver/internal/cast/splice"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"

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
	// rate changes how the delivery is made while the device plays it, nil for a delivery that cannot.
	rate switchable
}

// switchable is a delivery whose rate can change while the device plays it.
type switchable interface {
	// Switch makes the delivery as the plan for ceiling says from the returned byte on, zero where what was made stands.
	Switch(ctx context.Context, ceiling media.Bitrate) (from int64, err error)
	// Withheld says the delivery is holding itself back for the device.
	Withheld() bool
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
		Headers:       opts.delivery.Capabilities.ServedHeaders,
		IdleGrace:     idleGrace,
		WriteDeadline: writeDeadline,
	}
	splices := f.buffered != nil && f.buffered.reader.index != nil && burn == nil && opts.delivery.Plan.Splices()
	var rungs *ladder
	if splices && f.buffered.transcript == nil {
		rungs = s.ladder(work)
	}
	dir := filepath.Join(work, "delivery")
	// A burn-in follows the encoder's progress, so only a cast without one can do without an encoder.
	if f.buffered != nil && burn == nil && opts.Verbatim() {
		if relay, ok := opts.delivery.Plan.(output.Relayer); ok {
			return s.relay(relay, o, f, opts, dir, splices, rungs)
		}
	}

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
	prepared, err := opts.delivery.Plan.Prepare(o, dir)
	if err != nil {
		return delivery{}, err
	}
	s.releases.push(prepared.Output.Close)
	var d delivery
	if splices {
		// Published once wired, since the stream answers seeks through it.
		if d, err = s.produceChain(f, opts, encoderOpts, prepared, dir, rungs); err == nil {
			err = s.publish(&d)
		}
	} else {
		d = delivery{sink: prepared.Output}
		if err = s.publish(&d); err == nil {
			d.output, err = s.produceOnce(f, encoderOpts, prepared, dir, burn)
		}
	}
	if err != nil {
		return delivery{}, err
	}
	if prepared.WatchStorage != nil {
		s.watchStorage(prepared.WatchStorage)
	}
	return d, nil
}

// produceOnce runs one encoder over what the cast reads, for as long as the cast lasts.
func (s *preparation) produceOnce(f feed, encoderOpts encoding.EncodeOptions, prepared output.Prepared, dir string, burn burnRenderer) (producer, error) {
	var step func(media.Progress)
	if burn != nil {
		step = burn.Writer(s.ctx)
	}
	var tail io.ReadCloser
	if f.buffered != nil {
		var err error
		if tail, err = f.buffered.reader.spool.TailAt(s.ctx, 0); err != nil {
			return nil, err
		}
	}
	proc, err := s.startEncoder(encoderOpts, tail, step, dir)
	if err != nil {
		if tail != nil {
			_ = tail.Close()
		}
		return nil, err
	}
	production := startProduction(s.ctx, proc, tail, prepared.Write)
	s.releases.worker(production.stop, func() error {
		<-production.Done()
		return production.Err()
	})
	return production, nil
}

// publish serves the delivery's output; requests must stop before the storage they borrow is released.
func (s *preparation) publish(d *delivery) error {
	pub, err := s.Publisher.Publish(s.ctx, d.sink.Handler())
	if err != nil {
		return err
	}
	s.releases.publication(pub.Close)
	d.pub = pub
	return nil
}

// relay serves the read's spool, which the read keeps writing, as it is until the delivery must change.
func (s *preparation) relay(relay output.Relayer, o output.Opening, f feed, opts encodePlan, dir string, splices bool, rungs *ladder) (delivery, error) {
	reader := f.buffered.reader
	sink := relay.ServeSpool(o, reader.spool)
	s.releases.push(sink.Close)
	d := delivery{sink: sink, output: reader}
	if stream, ok := sink.(served); ok && splices {
		a := newAdjustable(s, s.stageSpec(opts, dir, rungs), dir, stream, sink.Audience(), reader, nil)
		d.output, d.rate = a, a
	}
	if err := s.publish(&d); err != nil {
		return delivery{}, err
	}
	slog.InfoContext(s.ctx, "serving the read's buffer without another encoder")
	return d, nil
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
	// A delivery that holds itself back for the device has made what the device took, and no more, by choice.
	if d.rate != nil && d.rate.Withheld() {
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

func (s *preparation) stageSpec(opts encodePlan, dir string, rungs *ladder) stageSpec {
	return stageSpec{
		plan: func(src *pull, ceiling media.Bitrate) (encoding.EncodeOptions, error) {
			facts := s.bufferedFacts(src)
			decided, err := s.decide(opts.delivery.Capabilities, opts.delivery.Plan.Format(), facts, "", false, ceiling, carried(opts.EncodeOptions))
			if err != nil {
				return encoding.EncodeOptions{}, err
			}
			return encoding.EncodeOptions{Input: encoding.FromPipe(opts.delivery.Plan.Pace(false)), Format: opts.delivery.Plan.EncoderFormat(), Probe: facts.probe, Video: decided.Video, Audio: decided.Audio, KeepTimeline: true}, nil
		},
		probe: func(src *pull) media.ProbeInfo { return s.bufferedFacts(src).probe },
		spawn: func(next encoding.EncodeOptions, from io.Reader) (*ffmpeg.Process, error) {
			return s.startEncoder(next, from, nil, dir)
		},
		ladder: rungs,
		warm:   &warmth{},
	}
}

// produceChain makes a delivery by encoders in turn over the read's buffer, each cut in where the last stopped.
func (s *preparation) produceChain(f feed, opts encodePlan, encoderOpts encoding.EncodeOptions, prepared output.Prepared, dir string, rungs *ladder) (delivery, error) {
	stream, ok := prepared.Output.(served)
	if !ok {
		return delivery{}, fmt.Errorf("a delivery made by encoders in turn has an output that cannot be carried on")
	}
	reader := f.buffered.reader
	encoderOpts.KeepTimeline = true
	in, err := splice.Open(s.ctx, reader.spool, 0, nil, nil)
	if err != nil {
		return delivery{}, err
	}
	proc, err := s.startEncoder(encoderOpts, in, nil, dir)
	if err != nil {
		_ = in.Close()
		return delivery{}, err
	}
	audience := prepared.Output.Audience()
	handed := func() int64 { n, _ := audience.Handed(); return n }
	spec := s.stageSpec(opts, dir, rungs)
	chain := startStages(s.ctx, reader, newStage(proc, in, reader, encoderOpts), 0, handed, spec, prepared.Write)
	s.releases.worker(chain.stop, func() error {
		<-chain.Done()
		return chain.Err()
	})
	a := newAdjustable(s, spec, dir, stream, audience, reader, chain)
	return delivery{sink: prepared.Output, output: a, rate: a}, nil
}

// carried is the audio codec opts gives the device, which a change of rate keeps.
func carried(opts encoding.EncodeOptions) media.Codec {
	if audio, encoded := opts.Audio.Encode(); encoded {
		return audio.Codec
	}
	return opts.Probe.AudioCodec
}
