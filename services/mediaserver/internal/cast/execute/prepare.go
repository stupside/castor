package execute

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/compose"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

// preparation builds one presentation within the attempt's context and resource
// scope. It cannot command the device or transition the attempt state machine.
type preparation struct {
	Machinery
	Publisher    output.Publisher
	Timelines    Timelines
	Captions     *Captions
	MaxHeight    media.HeightCap
	capabilities media.Capabilities
	ctx          context.Context
	attempt      recovery.Attempt
	releases     *releases
	failures     chan<- error
	subtitles    Subtitles
	// caption is the source caption fetched once, nil where none can be shown.
	caption *fetchedCaption
}

func newPreparation(ctx context.Context, c Cast, a recovery.Attempt, releases *releases, failures chan<- error) *preparation {
	p := &preparation{Machinery: c.Machinery, Publisher: c.Publisher, Timelines: c.Timelines,
		Captions: c.Captions, MaxHeight: c.MaxHeight, ctx: ctx, attempt: a, releases: releases, failures: failures}
	if c.Device != nil {
		p.capabilities = c.Device.Capabilities()
	}
	if c.Transcription != nil {
		p.subtitles = c.Transcription(ctx, a.Rendition.AudioLanguage)
	}
	return p
}

// presentation is the result of preparation, handed to the coordinator only
// after preparation returns. Its resources remain in the attempt's ownership.
type presentation struct {
	captions    []media.Caption
	target      *url.URL
	contentType string
	source      bool
	readiness   presentationReadiness
	delivery    *delivery
	feed        feed
	reached     ran
}

// inspectedPresentation retains the selected reading and any validated local
// buffer. Only this result can be passed to output planning.
type inspectedPresentation struct {
	presentation
	kind compose.Kind
	work string
	burn burnRenderer
}

// plannedPresentation binds codec and delivery decisions to their input.
type plannedPresentation struct {
	inspectedPresentation
	options encodePlan
}

func (s *preparation) inspectPresentation() (p inspectedPresentation, err error) {
	if err := s.ctx.Err(); err != nil {
		return p, err
	}
	s.caption = s.fetchCaption()
	if err := s.ctx.Err(); err != nil {
		return p, err
	}
	p.kind = s.compose()
	p.reached.began = s.attempt.Program.Start
	p.captions = s.shownCaptions()
	if p.kind == compose.Handoff {
		primary, _ := s.attempt.Program.PrimaryInput()
		p.target, p.contentType, p.source = primary.URL, primary.ContentType, true
		return p, nil
	}
	work, program, err := s.prepare()
	if err != nil {
		return p, err
	}
	p.work = work
	p.feed = feed{program: program}
	if p.kind == compose.ReadOnce {
		buf, err := s.read(work, program)
		if err != nil {
			return p, err
		}
		p.reached.reached, p.reached.reader = health.Reading, buf.reader
		if err := gate(s.ctx, buf); err != nil {
			return p, err
		}
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		err = encoding.ValidateTracks(ctx, s.Probes.File(buf.reader.spool.ProbePath()), buf.expected, buf.reader.Done())
		cancel()
		if err != nil {
			return p, fmt.Errorf("validating buffered read: %w", err)
		}
		p.feed = feed{buffered: buf}
		if _, native := s.nativeOutput(p.feed); buf.transcript != nil && !native {
			p.burn = subtitle.NewCueFile(work, buf.transcript.Cues())
		}
	}
	return p, nil
}

func (s *preparation) planPresentation(inspected inspectedPresentation) (plannedPresentation, error) {
	p := plannedPresentation{inspectedPresentation: inspected}
	if err := s.ctx.Err(); err != nil {
		return p, err
	}
	if p.kind == compose.Handoff {
		p.readiness = sourceEligible
		return p, nil
	}
	opts, err := s.encode(p.feed, p.burn)
	if err != nil {
		return p, err
	}
	p.options = opts
	return p, nil
}

func (s *preparation) producePresentation(planned plannedPresentation) (presentation, error) {
	p := planned.presentation
	if err := s.ctx.Err(); err != nil {
		return p, err
	}
	d, err := s.produce(planned.work, p.feed, planned.options, planned.burn)
	if err != nil {
		return p, err
	}
	p.delivery = &d
	p.reached.reached = health.Opening
	if err := awaitArtifact(s.ctx, d); err != nil {
		return p, err
	}
	if err := s.validateOutput(d, planned.options, p.feed); err != nil {
		return p, err
	}
	p.target, p.contentType = d.URL(), planned.options.Format.ContentType
	p.readiness = outputValidated
	return p, nil
}

// prepare is the work directory and the program with its followed timelines, which a cast castor serves starts from.
func (s *preparation) prepare() (string, media.Program, error) {
	work, err := os.MkdirTemp("", "castor-")
	if err != nil {
		return "", media.Program{}, fmt.Errorf("creating work directory: %w", err)
	}
	s.releases.push(func() error { return os.RemoveAll(work) })
	program, err := s.follow(s.attempt.Program)
	return work, program, err
}

// compose is the kind of cast this attempt calls for.
func (s *preparation) compose() compose.Kind {
	shape := compose.Shape{
		Device:    s.playbackCapabilities(),
		Program:   s.attempt.Program,
		Delivery:  s.attempt.Delivery,
		Height:    s.attempt.SelfFetchHeight(),
		MaxHeight: s.MaxHeight,
	}
	kind := compose.For(shape)
	// Requested transcription needs a buffered PCM feed whenever no usable
	// native captions are shown, including sources that publish no caption track.
	if s.transcribing() {
		kind = compose.ReadOnce
	}
	slog.InfoContext(s.ctx, "cast composition", "composition", string(kind))
	return kind
}

// transcribing is a cast whose subtitles castor makes from the sound, since the source shows none.
func (s *preparation) transcribing() bool { return s.subtitles != nil && s.caption == nil }

// follow points the inputs whose timelines castor keeps at castor's republished playlists.
func (s *preparation) follow(program media.Program) (media.Program, error) {
	followed, unfollow, err := s.Timelines.Republish(s.ctx, program)
	if err != nil {
		return media.Program{}, &timelineUnreadable{err: err}
	}
	s.releases.push(unfollow)
	return followed, nil
}

// timelineUnreadable is a source whose timeline castor must keep and could not read.
type timelineUnreadable struct{ err error }

func (e *timelineUnreadable) Error() string {
	return "following the source's timeline: " + e.err.Error()
}
func (e *timelineUnreadable) Unwrap() error { return e.err }
