package codec

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/cast/container"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

var videoCarriageRefusals = []refusalRule{{
	// Container carriage asks whether ffmpeg's muxer has a stream type for the codec.
	reason: reasonContainerVideo,
	why: func(in Inputs) string {
		why, _ := container.Why(in.Probe, in.Into)
		return why
	},
	when: func(in Inputs) bool { return container.Uncarried(in.Probe, in.Into).Video },
}, {
	// One encoder gives the device one stream: a copy keeps each piece's own size and timestamps.
	reason: reasonSpliced,
	why:    says("the source is stitched from pieces encoded apart, whose parameters change at each seam"),
	when:   func(in Inputs) bool { return in.Spliced },
}, {
	// ffmpeg turns the frames when it decodes them, so only a re-encode lands upright.
	reason: reasonRotated,
	why:    says("the source is turned by a display matrix, which no container castor writes carries"),
	when:   func(in Inputs) bool { return in.Probe.VideoRotation != 0 },
}, {
	// Kept apart from container's refusal because they are different failures with different lifetimes.
	reason: reasonVideoCopyFailed,
	why:    says("a previous attempt's copy of this video track broke upstream, so it is decoded instead"),
	when:   func(in Inputs) bool { return in.Decode.Video },
}}

var videoRefusals = slices.Concat([]refusalRule{{
	// The device's advertised envelope is the only thing that makes a copy safe at all.
	reason: reasonDeviceVideo,
	why:    says("the device never advertised this video envelope"),
	when:   func(in Inputs) bool { return !in.Caps.CanCopyVideo(in.Probe) },
}}, videoCarriageRefusals, []refusalRule{{
	// The ceiling is the user's request and may not be bypassed; cost is stated after encoder is known.
	reason: reasonHeightLimit,
	why:    says("the source picture is taller than this cast's ceiling admits"),
	when:   func(in Inputs) bool { return !in.MaxHeight.Admits(in.Probe.VideoHeight) },
}, {
	reason: reasonInterlaced,
	why:    says("the picture is coded as fields and the device does not deinterlace"),
	when:   func(in Inputs) bool { return in.Probe.VideoInterlaced && !in.Caps.Deinterlaces },
}, {
	reason: reasonHDRPolicy,
	why:    says("nothing establishes that an arbitrary device engages HDR on a stream it was handed"),
	when:   func(in Inputs) bool { return in.Probe.VideoHDR },
}, {
	// A cue drawn into the picture needs decoded frames; a copy has no frames to draw on.
	reason: reasonSubtitleBurnIn,
	why:    says("this leg burns subtitles into the picture, which needs decoded frames to draw on"),
	when:   func(in Inputs) bool { return in.BurnIn != "" },
}})

func decideVideo(ctx context.Context, in Inputs) (Track[VideoEncode], []Refusal, error) {
	if in.Measured && in.Probe.VideoCodec == "" {
		return CopyVideo(), nil, nil
	}
	refused := refuse(videoRefusals, in)
	if len(refused) == 0 {
		return CopyVideo(), nil, nil
	}
	host := func(c media.Codec) (ffmpeg.Encoder, bool) { return in.Encoders(ctx, c) }
	enc, t, ok := selectVideoEncoder(in.Caps, host)
	if !ok {
		return Track[VideoEncode]{}, refused, noVideoEncoder(in.Caps, host)
	}
	return Encode(VideoEncode{
		Encoder:             enc,
		Deinterlace:         in.Probe.VideoInterlaced && !in.Caps.Deinterlaces,
		ToneMap:             in.Probe.VideoHDR,
		MaxFrameRate:        playbackFrameRate,
		Bitrate:             t.bitrate,
		Maxrate:             t.maxrate,
		Bufsize:             t.bufsize,
		MaxHeight:           in.MaxHeight,
		KeyframeIntervalSec: keyframeSeconds,
		SubtitleTextFile:    in.BurnIn,
	}), refused, nil
}

// playbackFrameRate is the most any device castor serves shows in a second; above it a re-encode drops frames.
const playbackFrameRate = 60

func selectVideoEncoder(caps media.Capabilities, selectEncoder func(media.Codec) (ffmpeg.Encoder, bool)) (ffmpeg.Encoder, videoTarget, bool) {
	for _, t := range videoTargets {
		if !caps.SupportsVideoCodec(t.codec) {
			continue
		}
		if enc, ok := selectEncoder(t.codec); ok && holdsRealtime(enc, t.codec) {
			return enc, t, true
		}
	}
	// No common strategy is safer than inventing support the device never advertised.
	return ffmpeg.Encoder{}, videoTarget{}, false
}

func holdsRealtime(enc ffmpeg.Encoder, codec media.Codec) bool {
	return enc.Hardware || codec == media.CodecH264
}

func noVideoEncoder(caps media.Capabilities, selectEncoder func(media.Codec) (ffmpeg.Encoder, bool)) error {
	// Deduplicated; a family may list one codec several times and a user learns nothing three times.
	advertised := make([]string, 0, len(caps.Video))
	for _, s := range caps.Video {
		if !slices.Contains(advertised, string(s.Codec)) {
			advertised = append(advertised, string(s.Codec))
		}
	}
	// Asked over EVERY target rather than only device-wanted ones; each proof is cached for the process.
	producible := make([]string, 0, len(videoTargets))
	declined := make([]string, 0, len(videoTargets))
	for _, t := range videoTargets {
		codec := t.codec
		enc, ok := selectEncoder(codec)
		switch {
		case !ok:
		case holdsRealtime(enc, codec):
			producible = append(producible, string(codec))
		case caps.SupportsVideoCodec(codec):
			declined = append(declined,
				fmt.Sprintf("%s is available here only as the software encoder %s, which castor does not run live", codec, enc.Name))
		}
	}
	why := ""
	if len(declined) > 0 {
		why = " (" + strings.Join(declined, "; ") + ")"
	}
	return fmt.Errorf("no video encoder for this device: it decodes %s, this host can encode %s, and the two do not meet%s",
		cmp.Or(strings.Join(advertised, ", "), "no video codec at all"),
		cmp.Or(strings.Join(producible, ", "), "none of the codecs castor targets"),
		why)
}

type videoTarget struct {
	codec                     media.Codec
	bitrate, maxrate, bufsize string
}

// floorVideo is the codec every device decodes, and the one a read-once buffer is produced in.
var floorVideo = videoTarget{codec: media.CodecH264, bitrate: "4M", maxrate: "4M", bufsize: "8M"}

// videoTargets is every codec castor encodes video to, in preference order.
var videoTargets = []videoTarget{
	{codec: media.CodecHEVC, bitrate: "2M", maxrate: "2M", bufsize: "4M"},
	floorVideo,
}

const keyframeSeconds = 2

const floorVideoQuality = 23
