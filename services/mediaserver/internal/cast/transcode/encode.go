// Package transcode builds the ffmpeg command lines a cast runs: the read into its buffer, and the encode a device is served.
package transcode

import (
	"fmt"

	"github.com/stupside/castor/services/mediaserver/internal/cast/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/fetch"
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
	Video  codec.Track[codec.VideoEncode] // Video axis of encode (zero = no decision, refused).
	Audio  codec.Track[codec.AudioEncode] // Audio axis of encode (zero = no decision, refused).
}

// Verbatim reports whether this encode would only re-mux the spool it reads into the same stream.
func (o EncodeOptions) Verbatim() bool {
	_, video := o.Video.Encode()
	_, audio := o.Audio.Encode()
	return o.Video.Decided() && o.Audio.Decided() && !video && !audio &&
		o.Input.piped && o.Format.Muxer == SpoolFormat.Muxer &&
		o.Format.Delivery == container.DeliverStream
}

func encodeInputArgs(in EncodeInput) []string {
	if !in.piped {
		return sourceInputArgs(in.source)
	}
	args := paceArgs(in.pace, ffmpeg.Binary{})
	args = append(args, demuxFlags...)
	return append(args, "-f", SpoolFormat.Muxer, "-i", ffmpeg.StdinPipe)
}

// encodeMapArgs maps the first video and first audio track explicitly, and optionally.
func encodeMapArgs(in EncodeInput) []string {
	if in.piped {
		return []string{"-map", "0:V:0?", "-map", "0:a:0?"}
	}
	return sourceMapArgs(in.source)
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
	if err := decidedTracks(opts.Video, opts.Audio); err != nil {
		return ffmpeg.Command{}, err
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

	args := []string{"-hide_banner", "-nostats"}
	args = append(args, hardwareInitArgs(opts.Video)...)
	args = append(args, encodeInputArgs(opts.Input)...)
	args = append(args, encodeMapArgs(opts.Input)...)
	args = append(args, codecs...)
	args = append(args, "-strict", "-2")
	args = append(args, output...)
	return ffmpeg.NewCommand(append(args, encodeOutputTarget(opts, burnIn)...)), nil
}
