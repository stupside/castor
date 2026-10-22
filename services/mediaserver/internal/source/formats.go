package source

import (
	"context"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// Formats is every format castor reads, in order, first match.
type Formats []Format

// Claiming is the reader of contentType: its format, or opaque when none claims it.
func (fs Formats) Claiming(contentType string) Reader {
	for _, f := range fs {
		if f.Identity().ContentType == contentType {
			return f
		}
	}
	return opaque{}
}

// Named is the content type a link's extension names, else "".
func (fs Formats) Named(u *url.URL) string {
	ext := strings.ToLower(path.Ext(u.Path))
	for _, id := range fs.identities() {
		if ext != "" && slices.Contains(id.Extensions, ext) {
			return id.ContentType
		}
	}
	return ""
}

// Announced is the content type a server's MIME type names, aliases and parameters included, else "".
func (fs Formats) Announced(mimeType string) string {
	if parsed, _, err := mime.ParseMediaType(mimeType); err == nil {
		mimeType = parsed
	}
	mimeType = strings.ToLower(mimeType)
	for _, id := range fs.identities() {
		if mimeType != "" && slices.Contains(id.MIMETypes, mimeType) {
			return id.ContentType
		}
	}
	return ""
}

func (fs Formats) identities() []Identity {
	ids := make([]Identity, 0, len(fs)+len(containers))
	for _, f := range fs {
		ids = append(ids, f.Identity())
	}
	return append(ids, containers...)
}

// sniffBytes is enough of a body to read a playlist's or manifest's grammar, and far less than a film.
const sniffBytes = 64 << 10

// Identify names what a link carries: its name first, else the grammar of its body, since a script serves a playlist under any name.
func (fs Formats) Identify(ctx context.Context, c Client, u *url.URL, headers http.Header) string {
	if ct := fs.Named(u); ct != "" {
		return ct
	}
	headers = headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set("Range", timeline.Range{Length: sniffBytes}.Header())
	body, _, _, err := c.Fetch(ctx, u, headers)
	if err != nil {
		return ""
	}
	// A server that ignores the Range answers the whole file, whose tail is not the grammar of a playlist.
	body = body[:min(len(body), sniffBytes)]
	for _, f := range fs {
		if f.Recognize(body) {
			return f.Identity().ContentType
		}
	}
	return ""
}

// InputArgs is how ffmpeg and ffprobe open an input of contentType, as its format says.
func (fs Formats) InputArgs(contentType string, segmentRetries int) []string {
	return fs.Claiming(contentType).InputArgs(segmentRetries)
}
