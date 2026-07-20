package media

import "slices"

// Renderer describes what a target device can play without help from us: the
// containers it accepts as-is over the network (so the source URL can be handed
// to it directly), and the video envelopes it decodes natively (so a matching
// source can be stream-copied instead of re-encoded). It is the single
// capability model both device types describe themselves with; each Device
// resolves its own (DLNA negotiates it over GetProtocolInfo, Chromecast reports
// its receiver profile) while this package owns the type and the matching rules.
type Renderer struct {
	Containers []string
	Video      []VideoSupport
}

// VideoSupport is one video envelope a renderer decodes natively. A probed
// source is copy-eligible when it matches at least one on the things that
// black-screen a TV outright: codec, profile, bit depth, and dynamic range
// (HDR is always rejected: over DLNA we can't tell whether the renderer engages
// it). Resolution is deliberately absent: it is the user's cast-quality
// preference (config max_height), applied at source selection and the copy gate,
// not something guessed from the renderer.
type VideoSupport struct {
	Codec     Codec
	Profiles  []string // nil = any profile
	BitDepths []int    // nil = {8}
}

// AcceptsContainer reports whether the device plays contentType directly over
// the network (the pass-through decision).
func (r Renderer) AcceptsContainer(contentType string) bool {
	return slices.Contains(r.Containers, contentType)
}

// CanCopyVideo reports whether a probed source video can be stream-copied to
// this renderer instead of re-encoded.
func (r Renderer) CanCopyVideo(v ProbeInfo) bool {
	return slices.ContainsFunc(r.Video, func(s VideoSupport) bool { return s.accepts(v) })
}

// SupportsCodec reports whether the renderer decodes codec c natively, and so
// whether the pipeline may target it when re-encoding.
func (r Renderer) SupportsCodec(c Codec) bool {
	return slices.ContainsFunc(r.Video, func(s VideoSupport) bool { return s.Codec == c })
}

func (s VideoSupport) accepts(v ProbeInfo) bool {
	if v.VideoCodec != s.Codec {
		return false
	}
	if len(s.Profiles) > 0 && !slices.Contains(s.Profiles, v.VideoProfile) {
		return false
	}
	depths := s.BitDepths
	if depths == nil {
		depths = []int{8}
	}
	if !slices.Contains(depths, v.VideoBitDepth) {
		return false
	}
	return !v.VideoHDR
}
