package execute

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// buffered is a read into castor's own spool, and the transcription it feeds (nil where it feeds none).
type buffered struct {
	reader     *pull
	transcript Transcript
	expected   media.TrackRequirements
	// captionVideoOffset locates the first video sample on the PCM timeline.
	// An unknown clock cannot safely carry separately timed generated captions.
	captionVideoOffset *time.Duration
	// live is a read of an edge that arrives at 1x, which no pause gets ahead of.
	live bool
}

// read buffers program under its own context, so releasing it stops the read and its transcription before the work dir goes.
func (s *preparation) read(work string, program media.Program) (*buffered, error) {
	ctx, stop := context.WithCancel(s.ctx)
	source, err := encoding.NewProgramSource(program, s.attempt.Fetch, s.Binary, s.InputArgs)
	if err != nil {
		stop()
		return nil, err
	}
	facts := measure(ctx, "the source this cast buffers", s.Probes.Source(program, source.ProbeInputs()))
	program = aligned(program, facts.probe.InputStarts)
	if source, err = encoding.NewProgramSource(program, s.attempt.Fetch, s.Binary, s.InputArgs); err != nil {
		stop()
		return nil, err
	}

	floor, err := codec.Floor(ctx, codec.Inputs{
		Probe:     facts.probe,
		Measured:  facts.measured,
		Into:      encoding.SpoolFormat,
		Decode:    s.attempt.Decode,
		MaxHeight: s.MaxHeight,
		Spliced:   program.Seamed(),
		Encoders:  s.Encoders,
	})
	if err != nil {
		stop()
		return nil, err
	}
	logRefusals(ctx, floor)

	transcript := s.transcription(ctx, facts, s.attempt.Program)
	var pcmRate int
	if transcript != nil {
		pcmRate = transcript.SampleRate()
	}
	live := slices.ContainsFunc(program.Inputs, func(in media.Input) bool { return in.Fetching().Live })
	reader, err := startPull(ctx, pullSpec{
		live:       live,
		ffmpegPath: s.FFmpegPath,
		source:     source,
		probe:      facts.probe,
		spoolPath:  filepath.Join(work, "spool"+encoding.SpoolFormat.Extension),
		floor:      floor,
		pcmRate:    pcmRate,
	})
	if err != nil {
		stop()
		return nil, err
	}

	var transcribing sync.WaitGroup
	if transcript != nil {
		transcribing.Go(func() { transcript.Run(ctx, reader.pcm) })
	}
	s.releases.push(reader.spool.Close)
	s.releases.worker(stop, func() error {
		<-reader.Done()
		transcribing.Wait()
		return reader.Err()
	})
	var videoOffset *time.Duration
	if track, ok := program.Track(media.TrackVideo); ok && facts.probe.VideoStartOffset != nil {
		offset := *facts.probe.VideoStartOffset + program.Offsets[track.Input]
		videoOffset = &offset
	}
	expected := facts.probe.RequiredTracks().Or(s.attempt.Program.RequiredTracks())
	return &buffered{reader: reader, transcript: transcript, expected: expected, captionVideoOffset: videoOffset, live: live}, nil
}

// transcription reads sound once; native subtitles and burn-in consume the same cues.
func (s *preparation) transcription(ctx context.Context, facts facts, program media.Program) Transcript {
	if s.subtitles == nil {
		return nil
	}
	if len(s.shown) > 0 {
		slog.InfoContext(ctx, "no transcribed subtitles for this cast: the source's own captions are shown")
		return nil
	}
	if !facts.sounds(program) {
		slog.InfoContext(ctx, "no subtitles for this cast: nothing shows the source carries sound")
		return nil
	}
	return s.subtitles(ctx)
}
