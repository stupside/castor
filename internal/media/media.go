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

// HLSInputArgs contains ffmpeg/ffprobe flags that relax extension checks
// for HLS playlists and DASH manifests.
var HLSInputArgs = []string{
	"-allowed_extensions", "ALL",
	"-allowed_segment_extensions", "ALL",
	"-extension_picky", "0",
	"-seg_format_options", "extension_picky=0",
}

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
	Headers     map[string]string
	Bandwidth   int64
	ContentType string
}

// StreamInfo holds metadata returned by ffprobe for a stream.
type StreamInfo struct {
	BitRate     int64
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

var mimeContentTypes = map[string]string{
	"video/mp4":                     MP4,
	"video/webm":                    WebM,
	"video/x-matroska":              MKV,
	"audio/mpegurl":                 HLS,
	"audio/x-mpegurl":               HLS,
	"application/x-mpegurl":         HLS,
	"application/vnd.apple.mpegurl": HLS,
}

// DetectFromMIME returns a content type based on a confirmed MIME type,
// or empty string if unrecognized.
func DetectFromMIME(mime string) string {
	return mimeContentTypes[strings.ToLower(mime)]
}

// FormatHTTPHeaders formats a map of headers into the ffmpeg/ffprobe
// -headers flag value: "Key: Value\r\nKey2: Value2\r\n".
func FormatHTTPHeaders(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	var b strings.Builder
	for k, v := range headers {
		if strings.HasPrefix(k, ":") { // skip HTTP/2 pseudo-headers (:method, :path, …)
			continue
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\r\n")
	}
	return b.String()
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
