// Package streaminfo describes captured media links and their document evidence.
// It belongs to scrapingserver and has no device, probing, or playback dependencies.
package streaminfo

import (
	"encoding/xml"
	"io"
	"math"
	mimepkg "mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	HLS  = "application/x-mpegURL"
	DASH = "application/dash+xml"
	MP4  = "video/mp4"
)

func IsSegmented(t string) bool { return t == HLS || t == DASH }

type Ladder int

const (
	LadderUnknown Ladder = iota
	LadderMultivariant
	LadderSole
)

func (l Ladder) String() string {
	switch l {
	case LadderMultivariant:
		return "multivariant"
	case LadderSole:
		return "sole"
	}
	return "unknown"
}

type Stream struct {
	URL         *url.URL
	Headers     http.Header
	ContentType string
	Ladder      Ladder
	SourcePage  string
}
type Document struct {
	Ladder  Ladder
	Names   []*url.URL
	Runtime time.Duration
}

func ShorterThanContent(d time.Duration) bool { return d > 0 && d < 5*time.Minute }

// ContentTypeOf identifies the media type of a captured URL or response.
func ContentTypeOf(u *url.URL, mime string) string {
	if value, _, err := mimepkg.ParseMediaType(mime); err == nil {
		mime = value
	}
	if u != nil {
		switch strings.ToLower(path.Ext(u.Path)) {
		case ".m3u8":
			return HLS
		case ".mpd":
			return DASH
		case ".mp4", ".m4v":
			return MP4
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
	switch strings.ToLower(mime) {
	case "application/vnd.apple.mpegurl", "application/x-mpegurl", "audio/mpegurl", "audio/x-mpegurl":
		return HLS
	case DASH, MP4, "video/x-matroska", "video/webm", "video/x-msvideo", "video/quicktime", "video/mp2t", "video/x-flv":
		return strings.ToLower(mime)
	}
	return ""
}

var uri = regexp.MustCompile(`URI="([^"]*)"`)

// ParseDocument reads capture evidence and resolves references against the fetched URL.
func ParseDocument(body string, base *url.URL) Document {
	if len(body) > 2<<20 {
		return Document{}
	}
	body = strings.TrimSpace(strings.TrimPrefix(body, "\ufeff"))
	var d Document
	addRef := func(ref string, relativeTo *url.URL) {
		if relativeTo != nil && ref != "" && len(ref) <= 8192 {
			if u, err := relativeTo.Parse(ref); err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") && len(d.Names) < 8192 {
				d.Names = append(d.Names, u)
			}
		}
	}
	first, _, _ := strings.Cut(body, "\n")
	if strings.TrimSpace(first) == "#EXTM3U" {
		d.Ladder = LadderSole
		var duration float64
		ended := false
		for line := range strings.Lines(body) {
			line = strings.TrimSpace(line)
			if line == "#EXT-X-ENDLIST" {
				ended = true
			}
			if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") || strings.HasPrefix(line, "#EXT-X-I-FRAME-STREAM-INF:") {
				d.Ladder = LadderMultivariant
			}
			if after, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
				value, _, _ := strings.Cut(after, ",")
				n, err := strconv.ParseFloat(value, 64)
				if err == nil && n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0) && duration+n < float64(math.MaxInt64)/float64(time.Second) {
					duration += n
				} else {
					duration = math.NaN()
				}
			}
			if line != "" && !strings.HasPrefix(line, "#") {
				addRef(line, base)
			}
			for _, m := range uri.FindAllStringSubmatch(line, -1) {
				addRef(m[1], base)
			}
		}
		if ended && d.Ladder == LadderSole && !math.IsNaN(duration) {
			d.Runtime = time.Duration(duration * float64(time.Second))
		}
		return d
	}
	decoder := xml.NewDecoder(strings.NewReader(body))
	// A BaseURL changes its containing scope, not sibling representations.
	type scope struct {
		inherited []*url.URL
		bases     []*url.URL
		hasBase   bool
	}
	var scopes []scope
	rootSeen, rootClosed := false, false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if rootClosed {
				d.Ladder = LadderMultivariant
				return d
			}
			return Document{}
		}
		if err != nil {
			return Document{}
		}
		if _, ok := token.(xml.EndElement); ok {
			if len(scopes) == 0 {
				return Document{}
			}
			scopes = scopes[:len(scopes)-1]
			rootClosed = len(scopes) == 0
			continue
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			if text, ok := token.(xml.CharData); ok && len(scopes) == 0 && strings.TrimSpace(string(text)) != "" {
				return Document{}
			}
			continue
		}
		if len(scopes) == 0 {
			if rootSeen || start.Name.Local != "MPD" {
				return Document{}
			}
			rootSeen = true
			scopes = append(scopes, scope{inherited: []*url.URL{base}, bases: []*url.URL{base}})
			continue
		}
		parent := &scopes[len(scopes)-1]
		if start.Name.Local == "BaseURL" {
			var value string
			if decoder.DecodeElement(&value, &start) != nil {
				return Document{}
			}
			value = strings.TrimSpace(value)
			if value != "" && !strings.Contains(value, "$") {
				if !parent.hasBase {
					parent.bases = nil
					parent.hasBase = true
				}
				for _, inherited := range parent.inherited {
					addRef(value, inherited)
					if inherited != nil && len(parent.bases) < 16 {
						if resolved, err := inherited.Parse(value); err == nil {
							parent.bases = append(parent.bases, resolved)
						}
					}
				}
			}
			continue
		}
		switch start.Name.Local {
		case "SegmentTemplate", "SegmentURL", "Initialization", "RepresentationIndex":
			for _, a := range start.Attr {
				switch a.Name.Local {
				case "media", "initialization", "sourceURL", "index":
					if a.Value != "" && !strings.Contains(a.Value, "$") {
						for _, base := range parent.bases {
							addRef(a.Value, base)
						}
					}
				}
			}
		}
		if len(scopes) >= 64 {
			return Document{}
		}
		scopes = append(scopes, scope{inherited: parent.bases, bases: parent.bases})
	}
}
