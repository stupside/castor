package encoding

import (
	"strconv"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// statsPeriod is how often a pull reports on itself.
const statsPeriod = 500 * time.Millisecond

var SpoolFormat = spoolFormat()

func spoolFormat() container.Format {
	f, ok := container.For(media.MPEGTS)
	if !ok {
		panic("the format registry has no entry for " + media.MPEGTS)
	}
	return f
}

// PullOptions configures the single upstream reader's command line.
type PullOptions struct {
	Source ProgramSource

	// Probe is the measurement the floor's copy decisions were made from.
	Probe media.ProbeInfo

	// Video and Audio are the two halves of the read.
	Video codec.Track[codec.VideoEncode]
	Audio codec.Track[codec.AudioEncode]

	Verbose bool

	// KeepTimeline keeps the source's timestamps, so reads from anywhere in a program, or of any rung, share one clock.
	KeepTimeline bool

	// PCMSampleRate is the audio sample rate of the PCM tee, zero for none.
	PCMSampleRate int
}

func pullReportArgs(verbose bool) []string {
	// Each line carries its level, so a fault quotes complaints rather than the last chatter; baseline "warning" and never "error".
	logLevel := "level+warning"
	if verbose {
		logLevel = "level+verbose"
	}

	// -progress on the runner's first extra pipe.
	return []string{"-nostats", "-loglevel", logLevel,
		"-progress", ffmpeg.ProgressPipe, "-stats_period", formatSeconds(statsPeriod)}
}

func pcmOutputArgs(opts PullOptions) []string {
	var args []string
	if audioMap, ok := sourceMap(opts.Source, media.TrackAudio); ok {
		args = append(args, "-map", audioMap)
	}
	args = append(args, "-vn")
	args = append(args, opts.Source.outputArgs()...)
	// Raw PCM has no timestamps: materialize delayed starts and gaps as silence
	// on FFmpeg's normalized program clock before Whisper counts samples.
	return append(args,
		"-af", "aresample=async=1:first_pts=0",
		"-ac", "1",
		"-ar", strconv.Itoa(opts.PCMSampleRate),
		"-f", "s16le", ffmpeg.PCMPipe,
	)
}

func PullArgs(opts PullOptions) (ffmpeg.Command, error) {
	// A stream copy on each axis, or the floor encode the spool container needs.
	codecs, output, err := trackArgs(opts.Probe, SpoolFormat, opts.Video, opts.Audio)
	if err != nil {
		return ffmpeg.Command{}, err
	}

	args := pullReportArgs(opts.Verbose)
	if opts.KeepTimeline {
		args = append(args, "-copyts")
	}
	args = append(args, hardwareInitArgs(opts.Video)...)
	args = append(args, sourceInputArgs(opts.Source, opts.KeepTimeline)...)
	args = append(args, sourceMapArgs(opts.Source, opts.Video.Action() != codec.OmitTrack, opts.Audio.Action() != codec.OmitTrack)...)
	args = append(args, codecs...)
	args = append(args, output...)
	args = append(args, opts.Source.outputArgs()...)
	args = append(args, "-f", SpoolFormat.Muxer, ffmpeg.StdoutPipe)

	if opts.PCMSampleRate > 0 {
		args = append(args, pcmOutputArgs(opts)...)
	}
	return ffmpeg.NewCommand(args), nil
}
