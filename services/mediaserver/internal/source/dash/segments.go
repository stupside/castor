package dash

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Eyevinn/dash-mpd/mpd"

	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// edge is where a live presentation stands: which segments exist yet, and how far back they are kept.
type edge struct {
	live      bool
	now       time.Time
	available time.Time
	depth     time.Duration
}

// indexer reads the byte range of a resource a SegmentBase indexes its segments in.
type indexer func(uri string, r timeline.Range) ([]byte, error)

// addressed is one representation in one Period, with everything it inherits settled.
type addressed struct {
	period placed
	set    *mpd.AdaptationSetType
	rep    *mpd.RepresentationType
	base   *url.URL
	// named is a BaseURL stated at some level, rather than the presentation's own address standing in.
	named bool
}

// segments lists what the representation publishes in its Period, as the presentation states it at the edge.
func (a addressed) segments(e edge, fetch indexer) ([]timeline.Segment, error) {
	switch t, l, b := a.template(), a.list(), a.segmentBase(); {
	case t != nil:
		return a.templated(t, e)
	case l != nil:
		return a.listed(l)
	case b != nil:
		return a.indexed(b, fetch)
	case a.named:
		// A representation naming no addressing is one file: the whole Period in a single segment.
		return []timeline.Segment{{URI: a.base.String(), Duration: a.period.duration, Place: a.place(0, 1)}}, nil
	default:
		return nil, fmt.Errorf("representation %q names neither segments nor a file", a.rep.Id)
	}
}

func (a addressed) place(start, end int64) timeline.Place {
	return timeline.Place{Period: a.period.key(), Start: start, End: end}
}

// elapsed is how long the representation's Period has been running at the edge's clock.
func (a addressed) elapsed(e edge) time.Duration { return e.now.Sub(e.available.Add(a.period.start)) }

func value[T uint32 | uint64](p *T, fallback int64) int64 {
	if p == nil {
		return fallback
	}
	return int64(*p)
}

// initialization is the init section an Initialization element names, on the representation's own resource when it names none.
func (a addressed) initialization(i *mpd.URLType) (*timeline.Map, error) {
	if i == nil {
		return nil, nil
	}
	u := a.base
	if i.SourceURL != "" {
		parsed, err := a.base.Parse(string(i.SourceURL))
		if err != nil {
			return nil, err
		}
		u = parsed
	}
	return &timeline.Map{URI: u.String(), Range: byteRange(i.Range)}, nil
}

// byteRange reads first-last, inclusive, as DASH writes it.
func byteRange(s string) timeline.Range {
	first, last, ok := strings.Cut(s, "-")
	a, errA := strconv.ParseInt(strings.TrimSpace(first), 10, 64)
	b, errB := strconv.ParseInt(strings.TrimSpace(last), 10, 64)
	if !ok || errA != nil || errB != nil || b < a {
		return timeline.Range{}
	}
	return timeline.Range{Offset: a, Length: b - a + 1}
}

func ticks(n, timescale int64) time.Duration {
	return time.Duration(float64(n) / float64(timescale) * float64(time.Second))
}

var errNoEdge = errors.New("the live presentation states no availabilityStartTime, so nothing places its edge")

// addressed settles an offered representation's addressing inside its Period and presentation.
func (m presentation) addressed(from *url.URL, p placed, o offered) addressed {
	levels := [][]*mpd.BaseURLType{m.BaseURL, p.BaseURLs, o.set.BaseURLs, o.rep.BaseURLs}
	return addressed{
		period: p, set: o.set, rep: o.rep,
		base:  resolveBase(from, levels...),
		named: slices.ContainsFunc(levels, func(l []*mpd.BaseURLType) bool { return len(l) > 0 }),
	}
}

// inherit copies the nearest stated level, then fills what it leaves unstated from each level above.
func inherit[T any](fill func(into, from *T), levels ...*T) *T {
	var out *T
	for _, l := range levels {
		switch {
		case l == nil:
		case out == nil:
			out = new(*l)
		default:
			fill(out, l)
		}
	}
	return out
}
