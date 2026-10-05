package extract

import (
	"mime"
	"net/url"
	"path"
	"strings"
)

const (
	hlsMIME  = "application/x-mpegURL"
	dashMIME = "application/dash+xml"
	mp4MIME  = "video/mp4"
)

func segmented(t string) bool { return t == hlsMIME || t == dashMIME }

// contentTypeOf identifies the media type of a captured URL or response.
func contentTypeOf(u *url.URL, contentType string) string {
	if value, _, err := mime.ParseMediaType(contentType); err == nil {
		contentType = value
	}
	if u != nil {
		switch strings.ToLower(path.Ext(u.Path)) {
		case ".m3u8":
			return hlsMIME
		case ".mpd":
			return dashMIME
		case ".mp4", ".m4v":
			return mp4MIME
		case ".mkv":
			return "video/x-matroska"
		case ".webm":
			return "video/webm"
		case ".avi":
			return "video/x-msvideo"
		case ".mov":
			return "video/quicktime"
		case ".ts":
			return "video/mp2t"
		}
	}
	switch strings.ToLower(contentType) {
	case "application/vnd.apple.mpegurl", "application/x-mpegurl", "audio/mpegurl", "audio/x-mpegurl":
		return hlsMIME
	case dashMIME, mp4MIME, "video/x-matroska", "video/webm", "video/x-msvideo", "video/quicktime", "video/mp2t", "video/x-flv":
		return strings.ToLower(contentType)
	}
	return ""
}
