// Package streaminfo describes captured media links and their document evidence.
// It belongs to scrapingserver and has no device, probing, or playback dependencies.
package streaminfo

import (
	"encoding/xml"
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
	var d Document
	addRef := func(ref string) {
		if base != nil {
			if u, err := base.Parse(ref); err == nil {
				d.Names = append(d.Names, u)
			}
		}
	}
	if strings.Contains(body, "#EXTM3U") {
		d.Ladder = LadderSole
		var duration float64
		for line := range strings.Lines(body) {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#EXT-X-STREAM-INF") || strings.HasPrefix(line, "#EXT-X-I-FRAME-STREAM-INF") {
				d.Ladder = LadderMultivariant
			}
			if after, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
				value, _, _ := strings.Cut(after, ",")
				n, err := strconv.ParseFloat(value, 64)
				if err == nil {
					duration += n
				}
			}
			if line != "" && !strings.HasPrefix(line, "#") {
				addRef(line)
			}
			for _, m := range uri.FindAllStringSubmatch(line, -1) {
				addRef(m[1])
			}
		}
		if strings.Contains(body, "#EXT-X-ENDLIST") && d.Ladder == LadderSole {
			d.Runtime = time.Duration(duration * float64(time.Second))
		}
		return d
	}
	if !strings.Contains(body, "<MPD") {
		return d
	}
	d.Ladder = LadderMultivariant
	decoder := xml.NewDecoder(strings.NewReader(body))
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "BaseURL" {
			var value string
			if decoder.DecodeElement(&value, &start) == nil {
				value = strings.TrimSpace(value)
				if value != "" && !strings.Contains(value, "$") {
					addRef(value)
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
						addRef(a.Value)
					}
				}
			}
		}
	}
	return d
}
