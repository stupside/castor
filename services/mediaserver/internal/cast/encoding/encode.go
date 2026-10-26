// Package encoding builds the ffmpeg command lines a cast runs: the read into its buffer, and the encode a device is served.
package encoding

import (
	"fmt"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// EncodeInput is what an encode reads: the zero value reads nothing and is refused.
type EncodeInput struct {
	piped  bool
	pace   fetch.Pace
	source ProgramSource
}

// FromPipe reads the spool fed to stdin, at pace.
func FromPipe(pace fetch.Pace) EncodeInput {
	return EncodeInput{piped: true, pace: pace}
}

// FromSource reads the network source on the terms of its fetch plan.
func FromSource(source ProgramSource) EncodeInput { return EncodeInput{source: source} }

type EncodeOptions struct {
	Input  EncodeInput
	Probe  media.ProbeInfo                // Measurement copy decisions were made from.
	Format container.Format               // Container to produce.
	Video  codec.Track[codec.VideoEncode] // Video axis of the encode.
	Audio  codec.Track[codec.AudioEncode] // Audio axis of the encode.
	// KeepTimeline keeps the input's timestamps, so encodes of one buffer from different places join into one timeline.
	KeepTimeline bool
}

// Verbatim reports whether this encode would only re-mux the spool it reads into the same stream.
func (o EncodeOptions) Verbatim() bool {
	video := o.Video.Action() == codec.CopyTrack || o.Video.Action() == codec.OmitTrack && !o.Probe.HasVideo()
	audio := o.Audio.Action() == codec.CopyTrack || o.Audio.Action() == codec.OmitTrack && !o.Probe.HasAudio()
	return video && audio && (o.Video.Action() == codec.CopyTrack || o.Audio.Action() == codec.CopyTrack) &&
		o.Input.piped && o.Format.Muxer == SpoolFormat.Muxer &&
		o.Format.Delivery == container.DeliverStream
}

func encodeInputArgs(in EncodeInput, keepTimeline bool) []string {
	if !in.piped {
		return sourceInputArgs(in.source, keepTimeline)
	}
	args := paceArgs(in.pace, false)
	args = append(args, demuxFlags...)
	return append(args, "-f", SpoolFormat.Muxer, "-i", ffmpeg.StdinPipe)
}

// encodeMapArgs maps the first video and first audio track explicitly, and optionally.
func encodeMapArgs(opts EncodeOptions) []string {
	video, audio := opts.Video.Action() != codec.OmitTrack, opts.Audio.Action() != codec.OmitTrack
	if opts.Input.piped {
		var args []string
		if video {
			args = append(args, "-map", "0:V:0?")
		}
		if audio {
			args = append(args, "-map", "0:a:0?")
		}
		return args
	}
	return sourceMapArgs(opts.Input.source, video, audio)
}

func encodeOutputTarget(opts EncodeOptions, burnIn string) []string {
	args := []string{"-progress", ffmpeg.ProgressPipe}
	if burnIn != "" {
		args = append(args, "-stats_period", "0.1")
	}
	if !opts.Input.piped {
		args = append(args, opts.Input.source.outputArgs()...)
	}

	args = append(args, "-f", opts.Format.Muxer)
	args = append(args, opts.Format.Tuning.Args...)
	return append(args, opts.Format.Tuning.Output)
}

func EncodeArgs(opts EncodeOptions) (ffmpeg.Command, error) {
	if !opts.Input.piped && len(opts.Input.source.program.Inputs) == 0 {
		return ffmpeg.Command{}, fmt.Errorf("encode has no input: build one with FromPipe or FromSource")
	}
	if opts.Format.Muxer == "" {
		return ffmpeg.Command{}, fmt.Errorf("no output container: EncodeOptions.Format is unset")
	}
	codecs, output, err := trackArgs(opts.Probe, opts.Format, opts.Video, opts.Audio)
	if err != nil {
		return ffmpeg.Command{}, err
	}

	// The burn-in is a property of the video re-encode, so it is empty on a copy by construction.
	var burnIn string
	if venc, ok := opts.Video.Encode(); ok {
		burnIn = venc.SubtitleTextFile
	}

	args := []string{"-hide_banner", "-nostats", "-loglevel", "level+warning"}
	if opts.KeepTimeline {
		args = append(args, "-copyts")
	}
	args = append(args, hardwareInitArgs(opts.Video)...)
	args = append(args, encodeInputArgs(opts.Input, opts.KeepTimeline)...)
	args = append(args, encodeMapArgs(opts)...)
	args = append(args, codecs...)
	args = append(args, "-strict", "-2")
	args = append(args, output...)
	return ffmpeg.NewCommand(append(args, encodeOutputTarget(opts, burnIn)...)), nil
}
