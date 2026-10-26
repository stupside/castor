package codec

import "slices"

// Reason is evidence that made stream-copying one axis unsafe or too heavy for the ceiling.
type Reason string

const (
	reasonDeviceVideo     Reason = "device-video-incompatible"
	reasonDeviceAudio     Reason = "device-audio-incompatible"
	reasonContainerVideo  Reason = "container-video-incompatible"
	reasonContainerAudio  Reason = "container-audio-incompatible"
	reasonVideoCopyFailed Reason = "video-copy-failed"
	reasonAudioCopyFailed Reason = "audio-copy-failed"
	reasonHeightLimit     Reason = "height-limit"
	reasonBitrateLimit    Reason = "bitrate-limit"
	reasonHDRPolicy       Reason = "hdr-policy"
	reasonInterlaced      Reason = "interlaced"
	reasonRotated         Reason = "rotated"
	reasonSampleRate      Reason = "sample-rate"
	reasonSpliced         Reason = "spliced"
	reasonSubtitleBurnIn  Reason = "subtitle-burn-in"

	// reasonBitrateFloor is a ceiling under the lightest cast castor makes, which is made anyway.
	reasonBitrateFloor Reason = "bitrate-floor"
)

// ceilingOnly is a copy refused for the ceiling alone, which stays a copy castor cannot encode: a ceiling lightens a cast, it never stops one.
func ceilingOnly(refused []Reason) bool { return slices.Equal(refused, []Reason{reasonBitrateLimit}) }

// refusalRule is one row of an axis's copy-refusal table, with reason and when it applies.
type refusalRule struct {
	reason Reason
	// when reports whether this row refuses this subject.
	when func(Inputs) bool
}

func refuse(table []refusalRule, in Inputs) []Reason {
	var fired []Reason
	for _, r := range table {
		if r.when(in) {
			fired = append(fired, r.reason)
		}
	}
	return fired
}
