package rank

import (
	"cmp"
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// height is what a probe measured of this candidate's picture, or zero when no measurement established one.
// For candidates that carry a ladder (master playlists), returns the best rendition height under the ceiling.
func height(c *source.Stream, ceiling media.HeightCap) int {
	if c.Probe == nil {
		return 0
	}
	// For master playlists, the probe's VideoHeight may be the highest rendition.
	// Use VideoHeights to find the best rendition under the ceiling.
	if c.Ladder == source.LadderMultivariant && len(c.Probe.VideoHeights) > 0 {
		best := 0
		for _, h := range c.Probe.VideoHeights {
			if ceiling.Admits(h) && h > best {
				best = h
			}
		}
		if best > 0 {
			return best
		}
		// No rendition under ceiling; fall back to measured height (will be filtered by exceedsCap)
	}
	return c.Probe.VideoHeight
}

// carriesLadder reports that this candidate's captured document advertises renditions.
func carriesLadder(c *source.Stream) bool { return c.Ladder == source.LadderMultivariant }

// unreadLadder reports a playlist whose renditions nobody could establish, so it may well be a master.
func unreadLadder(c *source.Stream) bool {
	return media.IsSegmented(c.ContentType) && c.Ladder == source.LadderUnknown
}

// byLadderEvidence orders a document that advertises renditions first, then one nobody could read.
func byLadderEvidence(a, b *source.Stream) int {
	evidence := func(c *source.Stream) int {
		switch {
		case carriesLadder(c):
			return 2
		case unreadLadder(c):
			return 1
		}
		return 0
	}
	return cmp.Compare(evidence(b), evidence(a))
}

// exceedsCap reports whether a candidate's measured height is a real ceiling above the cast's.
func exceedsCap(c *source.Stream, ceiling media.HeightCap) bool {
	if carriesLadder(c) || unreadLadder(c) {
		return false
	}
	return !ceiling.Admits(height(c, ceiling))
}

// spoken ranks a candidate's sound by the languages asked for; zero for none, so the picture decides between those.
func spoken(c *source.Stream, languages []string) int {
	best := 0
	if c.Probe != nil {
		for _, track := range c.Probe.AudioTracks {
			best = max(best, source.LanguageRank(track.Language, languages))
		}
	}
	return best
}

func preference(a, b *source.Stream, ceiling media.HeightCap, languages []string) int {
	return cmp.Or(
		// A measured link beats one admitted unmeasured.
		source.CompareBool(!a.LastResort, !b.LastResort),
		source.CompareBool(!exceedsCap(a, ceiling), !exceedsCap(b, ceiling)),
		// The language comes before any quality: a worse picture in the language asked for beats a better one in another.
		cmp.Compare(spoken(a, languages), spoken(b, languages)),
		// The only signal here about RECOVERY rather than the picture: a master carries rungs to fall back to.
		source.CompareBool(carriesLadder(a), carriesLadder(b)),
		// A height is evidence that the selected track is a picture; zero is unknown, not short.
		source.CompareBool(height(a, ceiling) > 0, height(b, ceiling) > 0),
		cmp.Compare(height(a, ceiling), height(b, ceiling)),
		cmp.Compare(a.Bitrate(), b.Bitrate()),
		// Smaller is deliberately preferred only as a reproducible final tie-break.
		cmp.Compare(b.URL.String(), a.URL.String()),
	)
}

// ranked is the ordering a cast walks: best first, every admitted candidate present.
func ranked(pool []*source.Stream, ceiling media.HeightCap, languages []string) []*source.Stream {
	order := slices.Clone(pool)
	slices.SortStableFunc(order, func(a, b *source.Stream) int { return preference(b, a, ceiling, languages) })
	return order
}
