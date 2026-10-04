// Package dash is castor's DASH reader: it translates a presentation into the segments castor republishes.
package dash

import (
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// Format is DASH as a source.Format.
type Format struct{}

var _ source.Format = Format{}

func (Format) Identity() source.Identity {
	return source.Identity{ContentType: media.DASH, Extensions: []string{".mpd"}, MIMETypes: []string{media.DASH}}
}

func (Format) InputArgs(int) []string {
	return []string{"-f", ffmpeg.FormatDASH, "-allowed_extensions", "ALL"}
}

// signature is the root element every presentation is written under.
const signature = "<MPD"

// Recognize identifies a presentation by its signature, including a truncated sniffed head.
func (Format) Recognize(body string) bool {
	return strings.Contains(body, signature)
}
