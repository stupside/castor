package media

// Codec is an ffmpeg canonical codec name: the value ffprobe reports for a
// stream and the vocabulary the planner, renderer capabilities, and encoders
// all share, so codecs travel as a type instead of bare strings. It names the
// abstract codec (H.264), not a concrete encoder; one codec can have several
// encoders (libx264, h264_vaapi, h264_videotoolbox).
type Codec string

const (
	CodecH264 Codec = "h264"
	CodecHEVC Codec = "hevc"
	CodecAV1  Codec = "av1"
)
