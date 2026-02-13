package media

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

const (
	MP4  = "video/mp4"
	MKV  = "video/x-matroska"
	WebM = "video/webm"
	AVI  = "video/x-msvideo"
	MOV  = "video/quicktime"
	HLS  = "application/x-mpegURL"
)

var extensionMap = map[string]string{
	".mp4":  MP4,
	".mkv":  MKV,
	".webm": WebM,
	".avi":  AVI,
	".mov":  MOV,
	".m3u8": HLS,
}

// Stream represents a resolved media stream.
type Stream struct {
	URL         *url.URL
	ContentType string
}

// StreamInfo holds metadata returned by ffprobe for a stream.
type StreamInfo struct {
	ContentType string
}

// FormatInfo describes an output format.
type FormatInfo struct {
	ContentType string
	Extension   string
}

// formatRegistry maps ffmpeg output format names to FormatInfo.
var formatRegistry = map[string]FormatInfo{
	"mpegts":   {ContentType: "video/mp2t", Extension: ".ts"},
	"mp4":      {ContentType: MP4, Extension: ".mp4"},
	"matroska": {ContentType: MKV, Extension: ".mkv"},
	"webm":     {ContentType: WebM, Extension: ".webm"},
}

// LookupFormat returns format info for an ffmpeg output format name.
func LookupFormat(name string) (FormatInfo, bool) {
	fi, ok := formatRegistry[name]
	return fi, ok
}

// DetectFromExtension returns a content type based on the URL's file extension,
// or empty string if unrecognized.
func DetectFromExtension(u *url.URL) string {
	ext := strings.ToLower(path.Ext(u.Path))
	return extensionMap[ext]
}

// FormatToContentType maps an ffprobe format_name to a content type.
func FormatToContentType(format string) (string, error) {
	for f := range strings.SplitSeq(format, ",") {
		switch strings.TrimSpace(f) {
		case "hls", "applehttp":
			return HLS, nil
		case "mp4":
			return MP4, nil
		case "matroska":
			return MKV, nil
		case "webm":
			return WebM, nil
		case "avi":
			return AVI, nil
		}
	}
	return "", fmt.Errorf("unknown format: %s", format)
}
