package encoding

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// axis is one decided track as the output carries it.
type axis struct {
	spec string // ffmpeg's stream specifier: "v" or "a"
	kind media.TrackKind
	name string // what -c names: "copy", or the encoder
	// codec is what lands in the container: the source's when copied, the encoder's otherwise.
	codec   media.Codec
	copying bool
	action  codec.Action
	refused bool // the tables already say this container will not carry it
	// encode is the encoder's own options, nil when copied.
	encode []string
}

func videoAxis(probe media.ProbeInfo, video codec.Track[codec.VideoEncode], refused media.Axes) axis {
	a := axis{spec: "v", kind: media.TrackVideo, name: video.Name(), codec: probe.VideoCodec, copying: video.Action() == codec.CopyTrack, action: video.Action(), refused: refused.Video}
	if venc, ok := video.Encode(); ok {
		a.codec, a.copying, a.encode = venc.Encoder.Codec, false, videoEncodeArgs(venc)
	}
	return a
}

func audioAxis(probe media.ProbeInfo, audio codec.Track[codec.AudioEncode], refused media.Axes) axis {
	a := axis{spec: "a", kind: media.TrackAudio, name: audio.Name(), codec: probe.AudioCodec, copying: audio.Action() == codec.CopyTrack, action: audio.Action(), refused: refused.Audio}
	if aenc, ok := audio.Encode(); ok {
		a.codec, a.copying, a.encode = aenc.Codec, false, audioEncodeArgs(aenc)
	}
	return a
}

func videoEncodeArgs(venc codec.VideoEncode) []string {
	args := slices.Clone(venc.Encoder.Flags)
	if venc.Profile != "" {
		args = append(args, "-profile:v", venc.Profile)
	}
	if venc.Level != "" {
		args = append(args, "-level:v", venc.Level)
	}
	if venc.ToneMap {
		args = append(args, "-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709")
	}
	if venc.Bitrate > 0 {
		args = append(args, "-b:v", venc.Bitrate.FFmpeg())
	}
	// A quality target rather than an average rate, which only the recovery floor asks for.
	if venc.Quality > 0 {
		args = append(args, "-crf", strconv.Itoa(venc.Quality))
	}
	// VBV cap: bound the instantaneous bitrate so the pacer's fixed send rate is a real ceiling.
	if venc.Maxrate > 0 {
		args = append(args, "-maxrate", venc.Maxrate.FFmpeg())
	}
	if venc.Bufsize > 0 {
		args = append(args, "-bufsize", venc.Bufsize.FFmpeg())
	}
	if venc.KeyframeIntervalSec > 0 {
		args = append(args, "-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", venc.KeyframeIntervalSec))
	}
	return args
}

func hardwareInitArgs(video codec.Track[codec.VideoEncode]) []string {
	venc, ok := video.Encode()
	if !ok {
		return nil
	}
	return venc.Encoder.InitArgs
}

type axisArgs struct {
	args       []string
	movFlags   []string
	outputArgs []string
}

// args renders the axis into format; an encode takes its output's adaptations too (omitting them breaks E-AC-3).
func (a axis) args(format container.Format) (axisArgs, error) {
	if a.action == codec.Unplanned {
		return axisArgs{}, fmt.Errorf("%s track has no plan", a.kind)
	}
	if a.action == codec.OmitTrack {
		return axisArgs{args: []string{"-" + a.spec + "n"}}, nil
	}
	cp := container.Adapt(a.kind, a.codec, format, a.copying)
	// A copy the tables already refuse must not be buildable.
	if a.copying && a.refused {
		return axisArgs{}, fmt.Errorf("copy of %q into %q on -c:%s is known not to be carriable; it should have been re-encoded",
			a.codec, format.ContentType, a.spec)
	}
	out := axisArgs{args: []string{"-c:" + a.spec, a.name}, movFlags: cp.MovFlags, outputArgs: cp.OutputArgs}
	// ffmpeg takes the whole chain as one comma separated value.
	if len(cp.Filters) > 0 {
		out.args = append(out.args, "-bsf:"+a.spec, strings.Join(cp.Filters, ","))
	}
	out.args = append(out.args, a.encode...)
	return out, nil
}

func audioEncodeArgs(aenc codec.AudioEncode) []string {
	var args []string
	if aenc.Resync {
		// The filter graph rebuilds at a seam and replays timestamps; each packet must follow the one before.
		args = append(args, "-bsf:a", "setts=pts=max(PTS\\,PREV_OUTPTS+DURATION):dts=max(DTS\\,PREV_OUTDTS+DURATION)")
	}
	if aenc.SampleRate > 0 {
		args = append(args, "-ar", strconv.Itoa(aenc.SampleRate))
	}
	if aenc.Channels > 0 {
		args = append(args, "-ac", strconv.Itoa(aenc.Channels))
	}
	if aenc.Bitrate > 0 {
		args = append(args, "-b:a", aenc.Bitrate.FFmpeg())
	}
	return args
}

// trackArgs renders both decided axes into format: codecs first, then the output options their adaptations add.
func trackArgs(probe media.ProbeInfo, format container.Format, video codec.Track[codec.VideoEncode], audio codec.Track[codec.AudioEncode]) (codecs, output []string, err error) {
	if video.Action() == codec.OmitTrack && audio.Action() == codec.OmitTrack {
		return nil, nil, fmt.Errorf("plan omits both video and audio")
	}
	refused := container.Uncarried(probe, format)
	v, err := videoAxis(probe, video, refused).args(format)
	if err != nil {
		return nil, nil, err
	}
	a, err := audioAxis(probe, audio, refused).args(format)
	if err != nil {
		return nil, nil, err
	}
	return slices.Concat(videoFilterArgs(video), v.args, a.args), slices.Concat(v.outputArgs, a.outputArgs, movFlagsArgs(format.Tuning.MovFlags, v.movFlags, a.movFlags)), nil
}

func movFlagsArgs(base []string, extra ...[]string) []string {
	if flags := slices.Concat(append([][]string{base}, extra...)...); len(flags) > 0 {
		return []string{"-movflags", "+" + strings.Join(flags, "+")}
	}
	return nil
}
