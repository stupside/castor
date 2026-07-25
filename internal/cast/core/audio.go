package core

import (
	"github.com/stupside/castor/internal/cast/ffmpeg"
	"github.com/stupside/castor/internal/media"
)

// Audio delivery is device-neutral: both the spool path and the network remux
// path re-encode audio only when they must, and both decide it the same way,
// from a probe of the source plus the renderer's advertised audio support. The
// bitrate targets here are just good audio points, not tied to any device
// (unlike ResolveVideo's targets, which are the renderer's decode budget).

// audioTarget is a surround re-encode target: the bitrate and the channel
// ceiling the codec carries, so a source above it (a 7.1 track targeting AC-3)
// folds down to what the codec supports instead of failing the encode.
type audioTarget struct {
	bitrate     string
	maxChannels int
}

// audioTargets is the re-encode target per surround codec, used when a
// multichannel source can't be stream-copied but the renderer advertises a Dolby
// codec (see ResolveAudio). Bitrates are the common 5.1 broadcast/disc points.
// maxChannels is 6 (5.1) for both: ffmpeg's native ac3 and eac3 encoders reject
// more than 5.1 (they don't implement E-AC-3's 7.1 dependent substreams), so a
// 7.1 source folds to 5.1 rather than failing the encode. Stereo AAC is the
// floor below these and is not a target here. Adding a surround codec the
// pipeline encodes to is one entry here.
var audioTargets = map[media.Codec]audioTarget{
	media.CodecAC3:  {bitrate: "448k", maxChannels: 6},
	media.CodecEAC3: {bitrate: "384k", maxChannels: 6},
}

// stereoAudioBitrate is the stereo AAC floor: the target every renderer decodes,
// used when the source can't be copied and no surround codec is advertised.
const stereoAudioBitrate = "256k"

// audioCodecPreference ranks surround re-encode targets, most efficient first.
// ResolveAudio consults it only for a multichannel source that can't be copied:
// the first codec the renderer advertises wins, keeping the 5.1/7.1 layout
// instead of downmixing. Stereo AAC is the floor below this and needs no entry.
var audioCodecPreference = []media.Codec{media.CodecEAC3, media.CodecAC3}

// ResolveAudio fills opts' audio fields from the renderer's advertised audio
// support and the source's probed track. It is the shared audio decision for
// both served paths (the read-once spool off its spool probe, the network remux
// off a source probe):
//
//  1. copy — the renderer decodes the source codec (and channel count), so a
//     5.1/7.1 track passes through untouched, no quality loss and no downmix;
//  2. surround re-encode — a multichannel source the renderer can't copy is
//     re-encoded to a Dolby codec it advertises (E-AC-3, else AC-3), keeping the
//     layout up to that codec's channel ceiling;
//  3. stereo AAC — the floor every renderer decodes, when neither applies (also
//     what a set advertising nothing surround-capable, or a failed probe's zero
//     ProbeInfo, gets — the pre-surround behaviour).
func ResolveAudio(opts *ffmpeg.EncodeOptions, caps media.Renderer, src media.ProbeInfo) {
	if caps.CanCopyAudio(src) {
		opts.AudioCodec = ffmpeg.CodecCopy
		return
	}
	if src.AudioChannels > 2 {
		for _, codec := range audioCodecPreference {
			t, ok := audioTargets[codec]
			if !ok || !caps.SupportsAudioCodec(codec) {
				continue
			}
			opts.AudioCodec = string(codec)
			opts.AudioBitrate = t.bitrate
			opts.AudioSampleRate = 48000
			opts.AudioChannels = min(src.AudioChannels, t.maxChannels)
			return
		}
	}
	opts.AudioCodec = string(media.CodecAAC)
	opts.AudioBitrate = stereoAudioBitrate
	opts.AudioSampleRate = 48000
	opts.AudioChannels = 2
}
