package captions

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// merge places timestamp-mapped cues on the same origin as the media.
func merge(pieces []string, origin time.Duration) string {
	var cues []cue
	seen := map[cue]struct{}{}
	anchor := origin
	const rollover = time.Duration(1<<33) * time.Second / 90000
	for _, piece := range pieces {
		parsed, offset, mapped := parse(piece)
		if mapped {
			// MPEG-TS wraps every 2^33 ticks. Unwrap each window near the last
			// established clock position, rather than resetting at every map.
			if len(parsed) > 0 {
				raw := parsed[0].start + offset
				delta := anchor - raw
				shift := delta / rollover * rollover
				if delta%rollover > rollover/2 {
					shift += rollover
				}
				if delta%rollover < -rollover/2 {
					shift -= rollover
				}
				offset += shift
				anchor = parsed[len(parsed)-1].end + offset
			}
			offset -= origin
		} else {
			offset = 0
		}
		for _, c := range parsed {
			c, ok := c.moved(offset)
			if !ok {
				continue
			}
			if _, duplicate := seen[c]; duplicate {
				continue
			}
			seen[c] = struct{}{}
			cues = append(cues, c)
		}
	}
	slices.SortStableFunc(cues, func(a, b cue) int {
		return cmp.Compare(a.start, b.start)
	})
	return render(cues)
}

// Shifted moves a WebVTT document earlier by by, for media that begins that far into the source.
func Shifted(vtt string, by time.Duration) string {
	cues, _, _ := parse(vtt)
	var kept []cue
	for _, c := range cues {
		if c, ok := c.moved(-by); ok {
			kept = append(kept, c)
		}
	}
	return render(kept)
}

// moved is c shifted by d, false once it ends before the media begins.
func (c cue) moved(d time.Duration) (cue, bool) {
	c.start, c.end = max(c.start+d, 0), c.end+d
	return c, c.end > 0
}

func render(cues []cue) string {
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	for _, c := range cues {
		fmt.Fprintf(&out, "%s --> %s%s\n%s\n\n", clock(c.start, '.'), clock(c.end, '.'), c.settings, c.text)
	}
	return out.String()
}
