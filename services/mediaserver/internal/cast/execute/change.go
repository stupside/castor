package execute

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/splice"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

const (
	// matchTolerance is how far apart two keyframes may lie to be the same picture of two rungs.
	matchTolerance = 20 * time.Millisecond

	// lookback is how far before a cut another rung is read from: a read begins at the segment holding the time asked, which can lie some way before it.
	lookback = 20 * time.Second

	// matchCuts is how many keyframes of the rung being left are tried for one the rung moved to has too.
	matchCuts = 8
)

// errNotTheSame is a rung that does not carry the same pictures and sound as the one being left.
var errNotTheSame = errors.New("the rung carries other media than the one it would replace")

// carriesSame reports that two reads carry the same codecs and sound, which is what a device can follow from one to the other.
func carriesSame(a, b media.ProbeInfo) bool {
	return a.VideoCodec == b.VideoCodec && a.AudioCodec == b.AudioCodec && a.AudioChannels == b.AudioChannels && a.AudioSampleRate == b.AudioSampleRate
}

// change is how a delivery is made from the next stage on: the read it carries on from, maybe another rung's, and the encode.
type change struct {
	spec stageSpec
	life context.Context
	from *pull
	to   *pull
	rung source.Rendition
	opts encoding.EncodeOptions
}

func (c *change) moved() bool { return c.to != c.from }

// begin plans the change to ceiling, on another rung of the source when the ladder says; offset is the byte of the read the delivery has passed.
func (sp stageSpec) begin(ctx, life context.Context, from *pull, offset func() int64, ceiling media.Bitrate) (*change, error) {
	if sp.ladder != nil {
		if rung, ok := sp.ladder.choose(ceiling); ok {
			c, err := sp.moveTo(ctx, life, from, offset, rung, ceiling)
			if err == nil {
				return c, nil
			}
			slog.WarnContext(ctx, "the cast could not move to another rung of its source, so it is made lighter from the one it reads", "rung", rung.Bitrate.String(), "error", err)
		}
	}
	opts, err := sp.plan(from, ceiling)
	if err != nil {
		return nil, fmt.Errorf("planning the cast for %s: %w", ceiling, err)
	}
	return &change{spec: sp, life: life, from: from, to: from, opts: opts}, nil
}

// moveTo starts reading rung from a little before the next cut of the read being left, so that it has the keyframe to meet it at.
func (sp stageSpec) moveTo(ctx, life context.Context, from *pull, offset func() int64, rung source.Rendition, ceiling media.Bitrate) (*change, error) {
	select {
	case <-from.Done():
		return nil, errors.New("the read is complete")
	default:
	}
	waiting, cancel := context.WithTimeout(ctx, cutWait)
	cut, err := from.index.Wait(waiting, offset()+cutMargin)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("finding a keyframe to cut at: %w", err)
	}
	first, _ := from.index.First()
	to, err := sp.ladder.open(ctx, rung, max(0, from.begins+cut.PTS-first-lookback), from)
	if err != nil {
		return nil, err
	}
	c, err := sp.arrive(ctx, life, from, to, rung, ceiling)
	if err != nil {
		to.stop()
		return nil, err
	}
	return c, nil
}

// arrive waits for the rung's first keyframe and plans the encode over what it carries.
func (sp stageSpec) arrive(ctx, life context.Context, from, to *pull, rung source.Rendition, ceiling media.Bitrate) (*change, error) {
	waiting, cancel := context.WithTimeout(ctx, cutWait)
	_, err := to.index.Wait(waiting, 0)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("the rung made no keyframe: %w", err)
	}
	if !carriesSame(sp.probe(from), sp.probe(to)) {
		return nil, errNotTheSame
	}
	opts, err := sp.plan(to, ceiling)
	if err != nil {
		return nil, err
	}
	return &change{spec: sp, life: life, from: from, to: to, rung: rung, opts: opts}, nil
}

// cut finds the first keyframe at or past offset that both reads have, and starts the stage taking over there.
func (c *change) cut(ctx context.Context, offset int64) (splice.Cut, *stage, error) {
	waiting, cancel := context.WithTimeout(ctx, cutWait)
	defer cancel()
	old, err := c.from.index.Wait(waiting, offset)
	if err != nil {
		return splice.Cut{}, nil, fmt.Errorf("finding a keyframe to cut at: %w", err)
	}
	if !c.moved() {
		st, err := cutStage(ctx, c.life, c.from, c.spec, c.opts, old, nil)
		return old, st, err
	}
	for range matchCuts {
		meet, err := c.to.index.Matching(waiting, old.PTS, matchTolerance)
		switch {
		case err == nil:
			// The rung is cut in at the picture, but not at the sound the one it replaces has already given the device.
			var skip splice.Trimmer
			if next, ok := c.from.index.SoundAfter(old.Offset); ok {
				skip = spoolFraming().Trimmer(meet.Tables, next)
			}
			st, err := cutStage(ctx, c.life, c.to, c.spec, c.opts, meet, skip)
			return old, st, err
		case errors.Is(err, splice.ErrNoMatch):
			if old, err = c.from.index.Wait(waiting, old.Offset+1); err != nil {
				return splice.Cut{}, nil, fmt.Errorf("finding a keyframe to cut at: %w", err)
			}
		default:
			return splice.Cut{}, nil, fmt.Errorf("finding the keyframe of the rung: %w", err)
		}
	}
	return splice.Cut{}, nil, errors.New("the rung has no keyframe where the one it replaces has")
}

// makes reports that the stage making opts already makes what the change plans.
func (c *change) makes(opts encoding.EncodeOptions) bool {
	return reflect.DeepEqual(c.opts.Video, opts.Video) && reflect.DeepEqual(c.opts.Audio, opts.Audio)
}

func (c *change) attrs() []any {
	if !c.moved() {
		return nil
	}
	return []any{"rung", c.rung.Bitrate.String(), "height", c.rung.Height}
}

// done says the delivery was cut as planned: the read it left is stopped, and what is said of the read is said of the rung's.
func (c *change) done() {
	if c.moved() {
		c.from.continueWith(c.to)
		c.spec.ladder.arrived(c.rung)
	}
}

// abandon drops what a change started that was not cut in.
func (c *change) abandon() {
	if c.moved() {
		c.to.stop()
	}
}
