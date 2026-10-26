package hls

import (
	"cmp"
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// byAudioPreference keeps language priority across the whole ladder, not just
// within each variant's audio group. Quality breaks ties after language.
func byAudioPreference(languages []string) func(a, b source.Rendition) int {
	return func(a, b source.Rendition) int {
		return cmp.Or(cmp.Compare(source.LanguageRank(a.AudioLanguage, languages), source.LanguageRank(b.AudioLanguage, languages)), source.ByBitrate(a, b))
	}
}

// castable narrows a variant list to those that carry video (audio-only variants must not be picked).
func castable(doc document, languages []string) []variant {
	withVideo := slices.DeleteFunc(slices.Clone(doc.variants), func(v variant) bool {
		audio := doc.audioFor(v, languages).url
		return !v.hasVideo || (audio != nil && v.url.String() == audio.String())
	})
	return withVideo
}

// ladder is the choice the source offered: publication order preserved, audio-only rungs excluded.
func ladder(doc document, probe *media.ProbeInfo, languages ...string) []source.Rendition {
	measured := measuredHeights(doc, probe)
	rungs := castable(doc, languages)
	out := make([]source.Rendition, len(rungs))
	for i, v := range rungs {
		out[i] = rendition(doc, v, measured[v.program], languages)
	}
	return out
}

// measuredHeights is each variant's picture as a probe of this very master measured it, by program.
func measuredHeights(doc document, probe *media.ProbeInfo) map[int]int {
	if probe == nil || !doc.multivariant {
		return nil
	}
	// A probe that saw another program count read another document than the one parsed.
	variants := 0
	for _, v := range doc.variants {
		variants = max(variants, v.program+1)
	}
	if len(probe.ProgramHeights) != variants {
		return nil
	}
	return probe.ProgramHeights
}

// rendition translates one variant without dropping its companion audio; a probe of this very master wins over RESOLUTION, since it numbers variants as the master does.
func rendition(doc document, v variant, measured int, languages []string) source.Rendition {
	height := cmp.Or(measured, v.height)
	audio := doc.audioFor(v, languages)
	return source.Rendition{
		URL:           v.url,
		AudioURL:      audio.url,
		AudioLanguage: audio.language,
		Captions:      doc.captionsFor(v),
		Bitrate:       media.Bitrate(v.bandwidth),
		Height:        height,
		Declared:      v.declared,
	}
}
