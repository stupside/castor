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
	Audio      []AudioSupport

	// SelfFetch reports whether the renderer fetches an arbitrary stream URL
	// itself once it is handed one, versus needing castor to serve the bytes to
	// it. The planner reads this to choose pass-through (hand the device the
	// source URL and let it pull directly) over a castor-served stream:
	// Chromecast self-fetches, whereas DLNA only plays what we push to it via
	// SetAVTransportURI and so is always served locally.
	SelfFetch bool

	// ServedContainer is the content type castor produces when a self-fetching
	// renderer rejects the source container and must be served a live remux: the
	// container the local ffmpeg muxes and the device is told it is fetching.
	// Chromecast takes fragmented mp4; Roku cannot play a growing single-file URL
	// and takes live HLS instead. Empty defaults to media.MP4. Ignored for a
	// non-self-fetching renderer, whose served path is always the MPEG-TS spool.
	ServedContainer string
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

// AudioSupport is one audio codec a renderer decodes natively, up to MaxChannels
// channels. A probed audio track is copy-eligible when its codec matches and its
// channel count fits, so a 5.1/7.1 track passes through instead of being
// downmixed to stereo. MaxChannels 0 means "no advertised ceiling": trusted at
// full channel count, which is right for the inherently-surround Dolby codecs
// (AC-3/E-AC-3) whose advertised support already implies multichannel decode.
type AudioSupport struct {
	Codec       Codec
	MaxChannels int // highest channel count the renderer decodes; 0 = no ceiling
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

// SupportsCodec reports whether the renderer decodes video codec c natively, and
// so whether the pipeline may target it when re-encoding.
func (r Renderer) SupportsCodec(c Codec) bool {
	return slices.ContainsFunc(r.Video, func(s VideoSupport) bool { return s.Codec == c })
}

// CanCopyAudio reports whether a probed source audio track can be stream-copied
// to this renderer instead of re-encoded.
func (r Renderer) CanCopyAudio(p ProbeInfo) bool {
	return slices.ContainsFunc(r.Audio, func(s AudioSupport) bool { return s.accepts(p) })
}

// SupportsAudioCodec reports whether the renderer decodes audio codec c natively,
// and so whether the pipeline may re-encode a multichannel source to it rather
// than downmix to stereo.
func (r Renderer) SupportsAudioCodec(c Codec) bool {
	return slices.ContainsFunc(r.Audio, func(s AudioSupport) bool { return s.Codec == c })
}

func (s AudioSupport) accepts(p ProbeInfo) bool {
	if p.AudioCodec != s.Codec {
		return false
	}
	// An unknown source channel count (0) is trusted: a matching codec with no
	// probed layout is not worth force-transcoding. A known count above the
	// renderer's advertised ceiling is not copy-eligible.
	if s.MaxChannels > 0 && p.AudioChannels > s.MaxChannels {
		return false
	}
	return true
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
