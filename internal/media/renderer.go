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
// source is copy-eligible when it matches at least one. Zero-valued numeric
// fields mean "no constraint"; a nil BitDepths means 8-bit only. Every check is
// fail-closed: an unknown (zero) probe field never matches, so a failed probe
// falls back to a transcode.
type VideoSupport struct {
	Codec     Codec
	Profiles  []string // nil = any profile
	MaxLevel  int      // 0 = any (ffprobe level scale: 42 == H.264 level 4.2)
	MaxHeight int      // 0 = any
	BitDepths []int    // nil = {8}
	AllowHDR  bool
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
	if s.MaxLevel > 0 && (v.VideoLevel <= 0 || v.VideoLevel > s.MaxLevel) {
		return false
	}
	if s.MaxHeight > 0 && (v.VideoHeight <= 0 || v.VideoHeight > s.MaxHeight) {
		return false
	}
	depths := s.BitDepths
	if depths == nil {
		depths = []int{8}
	}
	if !slices.Contains(depths, v.VideoBitDepth) {
		return false
	}
	return s.AllowHDR || !v.VideoHDR
}
