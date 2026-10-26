package codec

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// audioTarget is a re-encode target: the bitrate and channel ceiling the codec carries.
type audioTarget struct {
	codec       media.Codec
	bitrate     media.Bitrate
	maxChannels int
}

// floorAudio is the stereo codec every device decodes.
var floorAudio = audioTarget{codec: media.CodecAAC, bitrate: 256_000, maxChannels: 2}

// surroundTargets is every surround codec castor encodes to, in preference order.
var surroundTargets = []audioTarget{
	{codec: media.CodecEAC3, bitrate: 384_000, maxChannels: 6},
	{codec: media.CodecAC3, bitrate: 448_000, maxChannels: 6},
}

var audioRefusals = slices.Concat([]refusalRule{{
	// Device's advertised support is codec AND channel count; 2.0 AC-3 is not 5.1 AC-3.
	reason: reasonDeviceAudio,
	when:   func(in Inputs) bool { return !in.Caps.CanCopyAudio(in.Probe) },
}, {
	reason: reasonSampleRate,
	when:   func(in Inputs) bool { return in.Probe.AudioSampleRate > media.PlaybackSampleRate },
}}, audioCarriageRefusals, []refusalRule{{
	reason: reasonBitrateLimit,
	when:   func(in Inputs) bool { return in.MaxBitrate > 0 && !copyFits(in) },
}})

// copyFits reports whether a copied sound is known to fit the ceiling: weighed alone, or within a whole copy proven under it.
func copyFits(in Inputs) bool {
	var picture media.Bitrate
	if hasPicture(in) {
		picture = minVideoRate
	}
	weighed := in.Probe.AudioBitrate > 0 && streamBudget(in.MaxBitrate)-in.Probe.AudioBitrate >= picture
	return weighed || in.MaxBitrate.Admits(in.Rate) && !encodesVideo(in)
}

var audioCarriageRefusals = []refusalRule{{
	reason: reasonSpliced,
	when:   func(in Inputs) bool { return in.Spliced },
}, {
	reason: reasonContainerAudio,
	when:   func(in Inputs) bool { return container.Uncarried(in.Probe, in.Into).Audio },
}, {
	reason: reasonAudioCopyFailed,
	when:   func(in Inputs) bool { return in.Decode.Audio },
}}

func decideAudio(in Inputs) (Track[AudioEncode], []Reason, error) {
	if !in.Probe.HasAudio() && in.Measured {
		return OmitAudio(), nil, nil
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
		for _, format := range in.Caps.Formats {
			support := format.Audio
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
	if ceilingOnly(refused) {
		return CopyAudio(), []Reason{reasonBitrateFloor}, nil
	}
	return Track[AudioEncode]{}, refused, fmt.Errorf("no supported audio codec for re-encoding %q", in.Probe.AudioCodec)
}

// weighs is what a planned sound adds to the cast; a copy nobody weighed is reserved the lightest target.
func weighs(in Inputs, audio Track[AudioEncode]) media.Bitrate {
	switch audio.Action() {
	case EncodeTrack:
		enc, _ := audio.Encode()
		return enc.Bitrate
	case CopyTrack:
		return cmp.Or(in.Probe.AudioBitrate, floorAudio.bitrate)
	}
	return 0
}
