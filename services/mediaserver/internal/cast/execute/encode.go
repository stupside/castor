package execute

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"

	"github.com/stupside/castor/internal/telemetry"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/cast/teardown"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// encodePlan keeps the producer instructions bound to the selected publisher.
type encodePlan struct {
	encoding.EncodeOptions
	delivery output.Selection
}

// encode decides codecs within the envelope accepted by the output adapter.
func (s *preparation) encode(f feed, burn burnRenderer) (encodePlan, error) {
	_, native := s.nativeOutput(f)
	var captions *output.Captions
	if native && burn == nil {
		captions = &output.Captions{Cues: f.buffered.transcript.Cues(), VideoOffset: *f.buffered.captionVideoOffset}
	}
	selections := s.Outputs.Selections(s.playbackCapabilities(), captions)
	if len(selections) == 0 {
		return encodePlan{}, fmt.Errorf("no delivery adapter accepts the requested playback envelopes")
	}

	var burnIn string
	if burn != nil {
		var err error
		if burnIn, err = burn.Create(); err != nil {
			return encodePlan{}, err
		}
	}

	var planningErr error
	for _, selected := range selections {
		into := selected.Plan.Format()
		pace := selected.Plan.Pace(burnIn != "")
		facts, input, err := s.input(f, pace)
		if err != nil {
			return encodePlan{}, err
		}
		decided, err := s.decide(selected.Capabilities, into, facts, burnIn, f.spliced())
		if err != nil {
			planningErr = errors.Join(planningErr, fmt.Errorf("%s: %w", into.ContentType, err))
			continue
		}
		return encodePlan{delivery: selected,
			Input: input, Format: into, Probe: facts.probe,
			Video: decided.Video, Audio: decided.Audio}, nil
	}
	return encodePlan{}, fmt.Errorf("no usable playback envelope: %w", planningErr)
}

// input is what the encoder reads from f, measured where it reads it: the spool as it grows, or the source itself.
func (s *preparation) input(f feed, ceiling fetch.Pace) (facts, encoding.EncodeInput, error) {
	if f.buffered != nil {
		measured := measure(s.ctx, "the local buffer this encode reads", s.Probes.File(f.buffered.reader.spool.ProbePath()))
		return measured, encoding.FromPipe(ceiling), nil
	}
	policies := s.attempt.Fetch.Encoding(f.program, ceiling)
	source, err := encoding.NewProgramSource(f.program, policies, s.Binary, s.InputArgs)
	if err != nil {
		return facts{}, encoding.EncodeInput{}, err
	}
	measured := measure(s.ctx, "the source this remux reads", s.Probes.Source(f.program, source.ProbeInputs()))
	if source, err = encoding.NewProgramSource(aligned(f.program, measured.probe.InputStarts), policies, s.Binary, s.InputArgs); err != nil {
		return facts{}, encoding.EncodeInput{}, err
	}
	return measured, encoding.FromSource(source), nil
}

func (s *preparation) decide(caps media.Capabilities, into container.Format, facts facts, burnIn string, spliced bool) (codec.Plan, error) {
	decided, err := codec.Served(s.ctx, codec.Inputs{
		Caps: caps, Probe: facts.probe, Measured: facts.measured, Into: into, MaxHeight: s.MaxHeight,
		Spliced: spliced,
		// Attempt's evidence: axis a reader already died copying not handed to second process to copy again.
		Decode:   s.attempt.Decode,
		BurnIn:   burnIn,
		Encoders: s.Encoders,
	})
	if err != nil {
		return codec.Plan{}, fmt.Errorf("planning media: %w", err)
	}

	slog.InfoContext(s.ctx, "encode decision",
		"video_codec", decided.Video.Name(),
		"audio_codec", decided.Audio.Name(),
		"source_video_codec", string(facts.probe.VideoCodec),
		"source_audio_codec", string(facts.probe.AudioCodec),
		"source_video", fmt.Sprintf("%s/%d/%d-bit", facts.probe.VideoProfile, facts.probe.VideoLevel, facts.probe.VideoBitDepth),
	)
	logRefusals(s.ctx, decided)
	observePlan(s.ctx, decided)
	if decided.DeviceRefusedVideo() {
		slog.InfoContext(s.ctx, "device video envelope", "accepts", deviceVideo(caps))
	}
	return decided, nil
}

// logRefusals states once why each axis a plan encodes is not copied.
func logRefusals(ctx context.Context, decided codec.Plan) {
	if len(decided.Refusals) > 0 {
		slog.InfoContext(ctx, "not copied", "reasons", decided.Refusals)
	}
}

// observePlan counts how each axis is carried and why a copy was refused.
func observePlan(ctx context.Context, decided codec.Plan) {
	encoded, copied := decided.Encoded(), decided.Copied()
	carry := func(axis telemetry.Axis, encode, copy bool) {
		switch {
		case encode:
			telemetry.MediaPlanned(ctx, axis, telemetry.Encode)
		case copy:
			telemetry.MediaPlanned(ctx, axis, telemetry.Copy)
		}
	}
	carry(telemetry.Video, encoded.Video, copied.Video)
	carry(telemetry.Audio, encoded.Audio, copied.Audio)
	for _, r := range decided.Refusals {
		telemetry.MediaRefused(ctx, string(r))
	}
}

// deviceVideo lists each video envelope the device accepts as codec/profiles/max level.
func deviceVideo(caps media.Capabilities) []string {
	var out []string
	for _, f := range caps.Formats {
		v := f.Video
		out = append(out, fmt.Sprintf("%s/%v/%d", v.Codec, v.Profiles, v.MaxLevel))
	}
	return slices.Compact(out)
}

// startEncoder runs opts in dir, reading tail where it reads a pipe (nil where it reads the source).
func (s *preparation) startEncoder(opts encoding.EncodeOptions, tail io.Reader, step func(media.Progress), dir string) (*ffmpeg.Process, error) {
	cmd, err := encoding.EncodeArgs(opts)
	if err != nil {
		return nil, fmt.Errorf("building encode args: %w", err)
	}

	slog.DebugContext(s.ctx, "encoder ffmpeg command", "path", s.FFmpegPath, "args", cmd.Args)

	proc, err := ffmpeg.Start(s.ctx, s.FFmpegPath, cmd, ffmpeg.Options{Stdin: tail, WorkDir: dir, Progress: step})
	if err != nil {
		return nil, fmt.Errorf("starting transcode: %w", err)
	}
	return proc, nil
}

func encoderResult(ctx context.Context, proc *ffmpeg.Process) error {
	err := cmp.Or(proc.Wait(), proc.SilentFailure())
	if ctx.Err() == nil && errors.Is(err, storage.ErrSpoolExpired) {
		return fmt.Errorf("encoder input expired: %w", err)
	}
	err = teardown.Failures(ctx, err)
	if err == nil || proc.Evidence().ExitStatus < 0 {
		return nil
	}
	if complaints := proc.Evidence().Lines; len(complaints) > 0 {
		return fmt.Errorf("encoder: %w: %s", err, strings.Join(complaints[max(0, len(complaints)-4):], " | "))
	}
	return fmt.Errorf("encoder: %w", err)
}
