package media

import (
	"fmt"
	"net/http"
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
	// HasVideo is true when the stream carries a decodable, non-image video
	// track (real dimensions, not a png/mjpeg placeholder). HasAudio is true
	// when it carries any audio track. Because probing uses the same ffmpeg
	// input path as the puller, these predict whether the puller's
	// "-map 0:v:0 -map 0:a:0" will succeed.
	HasVideo bool
	HasAudio bool
}

// Playable reports whether the stream carries castable media — a real video
// track plus audio. Decoy playlists (an image-only "video" track, or no audio)
// probe cleanly but cannot be remuxed, so they are not playable.
func (s StreamInfo) Playable() bool { return s.HasVideo && s.HasAudio }

type FormatInfo struct {
	ContentType string
	Extension   string
}

var formatRegistry = map[string]FormatInfo{
	"mpegts":   {ContentType: "video/mp2t", Extension: ".ts"},
	"mp4":      {ContentType: MP4, Extension: ".mp4"},
	"matroska": {ContentType: MKV, Extension: ".mkv"},
	"webm":     {ContentType: WebM, Extension: ".webm"},
}

// FormatForContentType returns the ffmpeg output format name and info for a
// content type, or ok=false if no producible format matches.
func FormatForContentType(ct string) (string, FormatInfo, bool) {
	for name, info := range formatRegistry {
		if info.ContentType == ct {
			return name, info, true
		}
	}
	return "", FormatInfo{}, false
}

var extensionMap = map[string]string{
	".mp4":  MP4,
	".mkv":  MKV,
	".webm": WebM,
	".avi":  AVI,
	".mov":  MOV,
	".m3u8": HLS,
}

// DetectFromExtension returns a content type based on the URL's file extension,
// or empty string if unrecognized.
func DetectFromExtension(u *url.URL) string {
	return extensionMap[strings.ToLower(path.Ext(u.Path))]
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

// FormatHTTPHeaders renders headers as ffmpeg's -headers flag value
// ("Key: Value\r\n…"), skipping HTTP/2 pseudo-headers that ffmpeg's HTTP
// stack won't accept.
func FormatHTTPHeaders(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	var b strings.Builder
	for k, v := range headers {
		if isPseudoHeader(k) {
			continue
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\r\n")
	}
	return b.String()
}

// ApplyHTTPHeaders copies headers onto req, skipping HTTP/2 pseudo-headers
// (:method, :path, …) which net/http won't accept.
func ApplyHTTPHeaders(req *http.Request, headers map[string]string) {
	for k, v := range headers {
		if isPseudoHeader(k) {
			continue
		}
		req.Header.Set(k, v)
	}
}

func isPseudoHeader(name string) bool { return strings.HasPrefix(name, ":") }

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
