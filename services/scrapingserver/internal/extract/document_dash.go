package extract

import (
	"encoding/xml"
	"io"
	"net/url"
	"strings"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

func inspectDASH(body string, base *url.URL) document {
	var d document
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
				d.ladder = castorv1.Ladder_LADDER_MULTIVARIANT
				return d
			}
			return document{}
		}
		if err != nil {
			return document{}
		}
		if _, ok := token.(xml.EndElement); ok {
			if len(scopes) == 0 {
				return document{}
			}
			scopes = scopes[:len(scopes)-1]
			rootClosed = len(scopes) == 0
			continue
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			if text, ok := token.(xml.CharData); ok && len(scopes) == 0 && strings.TrimSpace(string(text)) != "" {
				return document{}
			}
			continue
		}
		if len(scopes) == 0 {
			if rootSeen || start.Name.Local != "MPD" {
				return document{}
			}
			rootSeen = true
			scopes = append(scopes, scope{inherited: []*url.URL{base}, bases: []*url.URL{base}})
			continue
		}
		parent := &scopes[len(scopes)-1]
		if start.Name.Local == "BaseURL" {
			var value string
			if decoder.DecodeElement(&value, &start) != nil {
				return document{}
			}
			value = strings.TrimSpace(value)
			if value != "" && !strings.Contains(value, "$") {
				if !parent.hasBase {
					parent.bases = nil
					parent.hasBase = true
				}
				for _, inherited := range parent.inherited {
					d.addReference(value, inherited)
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
							d.addReference(a.Value, base)
						}
					}
				}
			}
		}
		if len(scopes) >= 64 {
			return document{}
		}
		scopes = append(scopes, scope{inherited: parent.bases, bases: parent.bases})
	}
}
