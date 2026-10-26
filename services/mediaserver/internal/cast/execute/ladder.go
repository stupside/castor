package execute

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// ladder is the versions of a program its source published, and what moves a cast's read between them while the device plays.
type ladder struct {
	s      *preparation
	stream *source.Stream
	rungs  []source.Rendition
	work   string

	mu      sync.Mutex
	current source.Rendition
	opened  int
}

// rungs is the versions of its program a cast can move between: those that declared what they weigh and the cast admits.
func (s *preparation) rungs() []source.Rendition {
	a := s.attempt
	if s.Resolver == nil || a.Stream == nil || a.Rendition.Bitrate == 0 {
		return nil
	}
	var usable []source.Rendition
	for _, r := range a.Origin.Renditions {
		// A rung in another language would change what the viewer hears.
		if r.Bitrate > 0 && s.MaxHeight.Admits(r.Height) && r.AudioLanguage == a.Rendition.AudioLanguage {
			usable = append(usable, r)
		}
	}
	if len(usable) < 2 {
		return nil
	}
	return usable
}

// ladder is nil where the cast has one version of its program.
func (s *preparation) ladder(work string) *ladder {
	rungs := s.rungs()
	if rungs == nil {
		return nil
	}
	return &ladder{s: s, stream: s.attempt.Stream, rungs: rungs, work: work, current: s.attempt.Rendition}
}

// choose is the rung to read for ceiling when it is not the one read now: the heaviest the ceiling admits, else the lightest there is.
func (l *ladder) choose(ceiling media.Bitrate) (source.Rendition, bool) {
	l.mu.Lock()
	current := l.current
	l.mu.Unlock()
	best := source.Origin{Renditions: l.rungs}.Choose(l.s.MaxHeight, ceiling, source.ByBitrate)
	return best, !best.Is(current)
}

func (l *ladder) arrived(rung source.Rendition) {
	l.mu.Lock()
	l.current = rung
	l.mu.Unlock()
}

// open starts reading rung from the program time at, on the clock of the read it takes over from.
func (l *ladder) open(ctx context.Context, rung source.Rendition, at time.Duration, from *pull) (*pull, error) {
	resolved, err := l.s.Resolver.Resolve(ctx, l.stream, rung, 0)
	if err != nil {
		return nil, fmt.Errorf("reading the rung: %w", err)
	}
	l.mu.Lock()
	l.opened++
	path := filepath.Join(l.work, fmt.Sprintf("spool-rung-%d%s", l.opened, encoding.SpoolFormat.Extension))
	l.mu.Unlock()
	return l.s.readAt(ctx, l.s.releases, resolved.Program, at, from, path, nil)
}

// readAt starts reading program from at into spool on the clock of from, released with rel; a known probe of the program spares measuring it again.
func (s *preparation) readAt(ctx context.Context, rel *releases, program media.Program, at time.Duration, from *pull, spool string, known *media.ProbeInfo) (*pull, error) {
	program.Start = at
	followed, err := s.follow(program)
	if err != nil {
		return nil, err
	}
	read, err := encoding.NewProgramSource(followed, s.attempt.Fetch, s.Binary, s.InputArgs)
	if err != nil {
		return nil, err
	}
	var probe media.ProbeInfo
	if known != nil {
		probe = *known
	} else {
		measured := measure(ctx, "the source this cast reads again", s.Probes.Source(followed, read.ProbeInputs()))
		if !measured.measured {
			return nil, fmt.Errorf("the source could not be measured to read it again")
		}
		probe = measured.probe
	}
	followed = aligned(followed, probe.InputStarts)
	if read, err = encoding.NewProgramSource(followed, s.attempt.Fetch, s.Binary, s.InputArgs); err != nil {
		return nil, err
	}
	pulled, err := startPull(s.ctx, pullSpec{
		ffmpegPath:   s.FFmpegPath,
		source:       read,
		probe:        probe,
		spoolPath:    spool,
		floor:        from.floor,
		keepTimeline: true,
		begins:       at,
	})
	if err != nil {
		return nil, err
	}
	rel.push(pulled.spool.Close)
	rel.worker(pulled.stop, pulled.joined)
	return pulled, nil
}
