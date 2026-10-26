package source

import (
	"cmp"
	"net/url"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Rendition is one version of a program a source offered, as the source described it.
type Rendition struct {
	URL *url.URL

	// Representation names the rung inside a manifest that publishes its ladder behind one URL.
	Representation string

	// AudioURL is the companion audio rendition for this rung.
	AudioURL *url.URL
	// AudioLanguage is the language of the selected audio track, when declared.
	AudioLanguage string

	// Captions are the text renditions the source published beside this rung.
	Captions []Caption

	// Bitrate is the rate the source declared, 0 when it declared none.
	Bitrate media.Bitrate

	// Height is the declared display height, 0 when the source omitted it.
	Height int

	// Declared is the codec envelope the source declared for this rung.
	Declared *media.Declaration
}

// Within reports that the rung is not known to outweigh the ceiling; a rung that declared no rate is left to the plan, which measures it.
func (r Rendition) Within(ceiling media.Bitrate) bool {
	return r.Bitrate == 0 || ceiling.Admits(r.Bitrate)
}

// ByBitrate orders rungs by the rate they declared, then by height: BANDWIDTH is the one attribute every HLS variant must state.
func ByBitrate(a, b Rendition) int {
	return cmp.Or(cmp.Compare(a.Bitrate, b.Bitrate), cmp.Compare(a.Height, b.Height))
}

func (r Rendition) Is(o Rendition) bool {
	sameURL := (r.URL == nil) == (o.URL == nil) && (r.URL == nil || r.URL.String() == o.URL.String())
	return r.Bitrate == o.Bitrate && r.Height == o.Height && r.Representation == o.Representation && sameURL
}

// Caption is one text rendition a source published: a WebVTT playlist, in a language.
type Caption struct {
	URL      *url.URL
	Language string
	Name     string
	Default  bool
	Forced   bool
}

// CompareBool orders false before true.
func CompareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case b:
		return -1
	}
	return 1
}
