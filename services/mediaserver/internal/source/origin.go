package source

import (
	"cmp"
	"slices"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Origin is what the source itself publishes about the program castor chose to read.
type Origin struct {
	Renditions []Rendition

	// Segmented reports that the program arrives as many small files rather than one long read.
	Segmented bool

	Framing media.Framing

	Live bool

	// Protection is the DRM the source declared, which castor cannot decrypt; empty for clear or AES-128 media.
	Protection string

	// Spliced reports a program stitched from pieces encoded apart, such as an ad pod.
	Spliced bool

	// Duration is the program's runtime as the source published it, 0 when it did not.
	Duration time.Duration
}

// Choose is the best rung within both ceilings, else the lightest within the height, else the shortest.
func (o Origin) Choose(height media.HeightCap, ceiling media.Bitrate, preferred func(a, b Rendition) int) Rendition {
	admitted := slices.DeleteFunc(slices.Clone(o.Renditions), func(r Rendition) bool { return !height.Admits(r.Height) })
	if len(admitted) == 0 {
		return slices.MinFunc(o.Renditions, func(a, b Rendition) int { return cmp.Compare(a.Height, b.Height) })
	}
	if light := slices.DeleteFunc(slices.Clone(admitted), func(r Rendition) bool { return !r.Within(ceiling) }); len(light) > 0 {
		return slices.MaxFunc(light, preferred)
	}
	// Every rung outweighs the ceiling: the lightest, which the plan then encodes down.
	return slices.MinFunc(admitted, ByBitrate)
}
