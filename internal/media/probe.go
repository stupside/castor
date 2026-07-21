package media

// ProbeInfo is a structured description of a source stream, the subset an
// ffprobe pass yields that the planner needs to decide whether the source can
// be stream-copied to a renderer or must be re-encoded. Zero values mean
// "unknown", which every capability check treats as "not safe to copy": a
// failed or partial probe always falls back to a transcode.
//
// It is produced by the ffmpeg adapter (ffmpeg.Probe) and consumed by a
// Renderer's copy check; the type lives here, in the domain, so neither side
// depends on the other.
type ProbeInfo struct {
	VideoCodec    Codec  // e.g. CodecH264, CodecHEVC
	VideoProfile  string // e.g. "High", "Main", "High 10"
	VideoHeight   int
	VideoBitDepth int  // derived from pix_fmt (8, 10, 12)
	VideoHDR      bool // PQ (smpte2084) or HLG (arib-std-b67) transfer

}
