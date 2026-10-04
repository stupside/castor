// Package hls is castor's HLS reader: resolves a playlist to the rendition a cast reads.
package hls

import (
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// Format is HLS as a source.Format.
type Format struct{}

var _ source.Format = Format{}

func (Format) Identity() source.Identity {
	return source.Identity{
		ContentType: media.HLS,
		Extensions:  []string{".m3u8"},
		MIMETypes:   []string{"application/vnd.apple.mpegurl", "application/x-mpegurl", "audio/mpegurl", "audio/x-mpegurl"},
	}
}

// InputArgs names the demuxer, since it refuses odd names, and reads unseekable, or it walks byte ranges without delivering them.
// Each segment opens its own connection: a proxy may close an idle one unannounced, and a reader reusing it hangs until its deadline.
func (Format) InputArgs(segmentRetries int) []string {
	return append([]string{"-f", ffmpeg.FormatHLS, "-http_seekable", "0", "-http_persistent", "0"}, ffmpeg.HLSTolerances(segmentRetries)...)
}

const (
	// signature is the required first line of every playlist.
	signature = timeline.TagHeader
	// renditionDeclaration declares a rendition with its own attributes (multivariant only).
	renditionDeclaration = "#EXT-X-STREAM-INF"
)

// Recognize identifies a playlist by its signature without parsing the whole document.
func (Format) Recognize(body string) bool {
	return strings.Contains(body, signature)
}
