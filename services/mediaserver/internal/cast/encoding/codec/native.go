package codec

import (
	"fmt"
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// originalTracks refuses before asking for an encoder: neither the buffer nor delivery may convert a native cast.
func originalTracks(in Inputs, videoRules, audioRules []refusalRule) (Plan, error) {
	if !in.Measured {
		return Plan{}, fmt.Errorf("%w: the source tracks could not be measured", media.ErrNativeIncompatible)
	}
	if !in.Probe.HasVideo() && !in.Probe.HasAudio() {
		return Plan{}, fmt.Errorf("%w: no video or audio was measured", media.ErrNativeIncompatible)
	}
	p := Plan{Video: OmitVideo(), Audio: OmitAudio()}
	if in.Probe.HasVideo() {
		p.Video = CopyVideo()
		p.Refusals = append(p.Refusals, originalRefusals(videoRules, in)...)
	}
	if in.Probe.HasAudio() {
		p.Audio = CopyAudio()
		p.Refusals = append(p.Refusals, originalRefusals(audioRules, in)...)
	}
	if len(p.Refusals) != 0 {
		return Plan{}, fmt.Errorf("%w: %v", media.ErrNativeIncompatible, p.Refusals)
	}
	return p, nil
}

// A trial leaves unknown decoder support to the receiver, not to a family default.
// Carriage, source integrity, explicit ceilings and subtitle requirements still bind.
func originalRefusals(rules []refusalRule, in Inputs) []Reason {
	refused := refuse(rules, in)
	if in.Caps.NativePlaybackTrial {
		refused = slices.DeleteFunc(refused, func(reason Reason) bool {
			switch reason {
			case reasonDeviceVideo, reasonDeviceAudio, reasonHDRPolicy, reasonInterlaced, reasonSampleRate:
				return true
			default:
				return false
			}
		})
	}
	return refused
}
