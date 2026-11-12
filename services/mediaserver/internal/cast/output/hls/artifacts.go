package hls

import (
	"path"
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// artifactContentType prevents net/http from sniffing generated media as text.
func artifactContentType(name string) (string, bool) {
	switch strings.ToLower(path.Ext(name)) {
	case ".m3u8":
		return media.HLS, true
	case segmentExt:
		return "video/iso.segment", true
	case ".mp4":
		return media.MP4, true
	default:
		return "", false
	}
}

// isMediaArtifact excludes initialization, playlists and temporary writes when
// observing whether a receiver has fetched playable media.
func isMediaArtifact(name string) bool {
	return name == path.Base(name) && strings.HasPrefix(name, segmentPrefix) && path.Ext(name) == segmentExt
}
