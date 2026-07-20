package media

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
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
	Headers     http.Header
	Bandwidth   int64
	ContentType string
}

// StreamInfo holds metadata returned by ffprobe for a stream.
type StreamInfo struct {
	BitRate     int64
	Duration    time.Duration
	ContentType string
	HasVideo    bool
	HasAudio    bool
	// VideoHeight is the display height of the real video track, 0 if unknown.
	// For an HLS master this is only whichever variant ffprobe chose, not the
	// master's full range, so it is not a reliable ceiling for a master.
	VideoHeight int
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

// NormalizeStreamHeaders returns a copy of browser-captured headers ready to
// replay to the puller. It drops headers that break a re-issued fetch and, when
// a Referer is present without an Origin, derives the Origin from it: a
// cross-origin browser GET sends only a Referer, but CDNs commonly gate segment
// delivery on Origin too, and the derived pair is what a site's own player proxy
// sends. The input is not mutated.
func NormalizeStreamHeaders(h http.Header) http.Header {
	out := h.Clone()
	if out == nil {
		return nil
	}
	// A stale Range fetches a byte slice instead of the whole resource; a
	// br/zstd Accept-Encoding yields a body ffmpeg can't decode ("Invalid data
	// found when processing input").
	out.Del("Range")
	out.Del("Accept-Encoding")
	if out.Get("Origin") == "" {
		if origin := originOf(out.Get("Referer")); origin != "" {
			out.Set("Origin", origin)
		}
	}
	return out
}

// originOf returns the scheme://host origin of an absolute URL, or "" if s is
// not one.
func originOf(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
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
