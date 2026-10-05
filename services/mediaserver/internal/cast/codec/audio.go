package codec

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/cast/container"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// audioTarget is a re-encode target: the bitrate and channel ceiling the codec carries.
type audioTarget struct {
	codec       media.Codec
	bitrate     string
	maxChannels int
}

// floorAudio is the stereo codec every device decodes.
var floorAudio = audioTarget{codec: media.CodecAAC, bitrate: "256k", maxChannels: 2}

// surroundTargets is every surround codec castor encodes to, in preference order.
var surroundTargets = []audioTarget{
	{codec: media.CodecEAC3, bitrate: "384k", maxChannels: 6},
	{codec: media.CodecAC3, bitrate: "448k", maxChannels: 6},
}

var audioRefusals = slices.Concat([]refusalRule{{
	// Device's advertised support is codec AND channel count; 2.0 AC-3 is not 5.1 AC-3.
	reason: reasonDeviceAudio,
	why:    says("the device never advertised this audio codec at this channel count"),
	when:   func(in Inputs) bool { return !in.Caps.CanCopyAudio(in.Probe) },
}, {
	reason: reasonSampleRate,
	why:    says(fmt.Sprintf("the audio is sampled above the %d Hz every device castor serves plays", media.PlaybackSampleRate)),
	when:   func(in Inputs) bool { return in.Probe.AudioSampleRate > media.PlaybackSampleRate },
}}, audioCarriageRefusals)

var audioCarriageRefusals = []refusalRule{{
	reason: reasonSpliced,
	why:    says("the source is stitched from pieces encoded apart, whose sample rates and timestamps change at each seam"),
	when:   func(in Inputs) bool { return in.Spliced },
}, {
	reason: reasonContainerAudio,
	why: func(in Inputs) string {
		_, why := container.Why(in.Probe, in.Into)
		return why
	},
	when: func(in Inputs) bool { return container.Uncarried(in.Probe, in.Into).Audio },
}, {
	reason: reasonAudioCopyFailed,
	why:    says("a previous attempt's copy of this audio track broke upstream, so it is decoded instead"),
	when:   func(in Inputs) bool { return in.Decode.Audio },
}}

func decideAudio(in Inputs) (Track[AudioEncode], []Refusal, error) {
	if in.Probe.AudioCodec == "" && in.Measured {
		return CopyAudio(), nil, nil
	}
	refused := refuse(audioRefusals, in)
	if len(refused) == 0 {
		return CopyAudio(), nil, nil
	}
	channels := cmp.Or(in.Probe.AudioChannels, 2)
	var targets []audioTarget
	if channels > 2 {
		// Surround first so its channels survive; stereo AAC before a surround codec for a stereo source.
		targets = append(slices.Clone(surroundTargets), floorAudio)
	} else {
		targets = append([]audioTarget{floorAudio}, surroundTargets...)
	}
	for _, t := range targets {
		outputChannels := 0
		for _, support := range in.Caps.Audio {
			if support.Codec != t.codec {
				continue
			}
			limit := t.maxChannels
			if support.MaxChannels > 0 {
				limit = min(limit, support.MaxChannels)
			}
			// Keep the stereo floor unless the device advertises a lower ceiling.
			outputChannels = max(outputChannels, min(max(channels, 2), limit))
		}
		if outputChannels == 0 {
			continue
		}
		return Encode(AudioEncode{
			Codec:      t.codec,
			Bitrate:    t.bitrate,
			SampleRate: media.PlaybackSampleRate,
			Channels:   outputChannels,
			Resync:     in.Spliced,
		}), refused, nil
	}
	return Track[AudioEncode]{}, refused, fmt.Errorf("no supported audio codec for re-encoding %q", in.Probe.AudioCodec)
}
