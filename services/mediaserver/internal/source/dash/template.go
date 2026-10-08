package dash

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/Eyevinn/dash-mpd/mpd"

	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// template is the SegmentTemplate the representation reads, each attribute from the nearest level that states it.
func (a addressed) template() *mpd.SegmentTemplateType {
	return inherit(func(out, t *mpd.SegmentTemplateType) {
		out.Media = cmp.Or(out.Media, t.Media)
		out.Initialization = cmp.Or(out.Initialization, t.Initialization)
		out.SegmentBaseType.Initialization = cmp.Or(out.SegmentBaseType.Initialization, t.SegmentBaseType.Initialization)
		out.StartNumber = cmp.Or(out.StartNumber, t.StartNumber)
		out.Timescale = cmp.Or(out.Timescale, t.Timescale)
		out.Duration = cmp.Or(out.Duration, t.Duration)
		out.PresentationTimeOffset = cmp.Or(out.PresentationTimeOffset, t.PresentationTimeOffset)
		out.AvailabilityTimeOffset = cmp.Or(out.AvailabilityTimeOffset, t.AvailabilityTimeOffset)
		out.SegmentTimeline = cmp.Or(out.SegmentTimeline, t.SegmentTimeline)
	}, a.rep.SegmentTemplate, a.set.SegmentTemplate, a.period.SegmentTemplate)
}

// early is how long before its last sample a segment may be asked for; INF, as soon as it starts.
func early(t *mpd.SegmentTemplateType, length time.Duration) time.Duration {
	if ato := float64(t.AvailabilityTimeOffset); !math.IsInf(ato, 1) {
		return time.Duration(ato * float64(time.Second))
	}
	return length
}

func (a addressed) templated(t *mpd.SegmentTemplateType, e edge) ([]timeline.Segment, error) {
	timescale, first, offset := value(t.Timescale, 1), value(t.StartNumber, 1), value(t.PresentationTimeOffset, 0)
	if timescale <= 0 {
		return nil, fmt.Errorf("representation %q states timescale %d", a.rep.Id, timescale)
	}
	var init *timeline.Map
	switch {
	case t.Initialization != "":
		u, err := a.base.Parse(a.expand(t.Initialization, 0, 0))
		if err != nil {
			return nil, err
		}
		init = &timeline.Map{URI: u.String()}
	case t.SegmentBaseType.Initialization != nil:
		var err error
		if init, err = a.initialization(t.SegmentBaseType.Initialization); err != nil {
			return nil, err
		}
	}
	add := func(out []timeline.Segment, number, at, d int64, place timeline.Place) ([]timeline.Segment, error) {
		u, err := a.base.Parse(a.expand(t.Media, number, at))
		if err != nil {
			return nil, err
		}
		return append(out, timeline.Segment{URI: u.String(), Duration: ticks(d, timescale), Map: init, Place: place}), nil
	}
	tick := func(d time.Duration) int64 { return int64(d.Seconds() * float64(timescale)) }
	periodEnd := int64(math.MaxInt64)
	if a.period.duration > 0 {
		periodEnd = offset + tick(a.period.duration)
	}

	var out []timeline.Segment
	var err error
	if t.SegmentTimeline != nil {
		// Live, a segment exists once it has been produced, and stays only as long as the time-shift buffer keeps it.
		kept := int64(math.MinInt64)
		if e.live {
			kept = offset + tick(a.elapsed(e)-e.depth)
		}
		number, at := first, int64(0)
		entries := t.SegmentTimeline.S
		for i, s := range entries {
			if s.T != nil {
				at = int64(*s.T)
			}
			d := int64(s.D)
			if d <= 0 {
				return nil, fmt.Errorf("representation %q lists a segment of duration %d", a.rep.Id, d)
			}
			repeats := int64(s.R)
			if repeats < 0 {
				// An open repeat runs to the next entry, or to the Period's end, or live, to the last segment produced.
				switch {
				case i+1 < len(entries) && entries[i+1].T != nil:
					repeats = max(0, (int64(*entries[i+1].T)-at+d-1)/d-1)
				case e.live:
					produced := min(periodEnd, offset+tick(a.elapsed(e)+early(t, ticks(d, timescale))))
					repeats = max(-1, (produced-at)/d-1)
				case periodEnd != math.MaxInt64:
					repeats = max(0, (periodEnd-at+d-1)/d-1)
				default:
					return nil, fmt.Errorf("representation %q repeats a segment without end", a.rep.Id)
				}
			}
			// Skip expired repeats arithmetically: a long-running timeline must not walk its entire history at every reload.
			count := repeats + 1
			skip := int64(0)
			if kept >= at+d {
				skip = min(count, (kept-at)/d)
			}
			until := count
			if periodEnd != math.MaxInt64 {
				until = min(until, max(0, (periodEnd-at+d-1)/d))
			}
			if e.live {
				produced := offset + tick(a.elapsed(e)+early(t, ticks(d, timescale)))
				until = min(until, max(0, (produced-at)/d))
			}
			for n := skip; n < until; n++ {
				start := at + n*d
				if out, err = add(out, number+n, start, d, a.place(start, start+d)); err != nil {
					return nil, err
				}
			}
			number, at = number+count, at+count*d
		}
		return out, nil
	}

	d := value(t.Duration, 0)
	length := ticks(d, timescale)
	if d <= 0 || length <= 0 {
		return nil, fmt.Errorf("representation %q names segments by neither a timeline nor a duration", a.rep.Id)
	}
	from, count := int64(0), int64(math.MaxInt64)
	if a.period.duration > 0 {
		count = int64(math.Ceil(float64(a.period.duration) / float64(length)))
	}
	if e.live {
		elapsed := a.elapsed(e)
		count = min(count, int64((elapsed+early(t, length))/length))
		from = max(0, int64((elapsed-e.depth)/length))
	}
	if count == math.MaxInt64 {
		return nil, fmt.Errorf("representation %q numbers segments without end", a.rep.Id)
	}
	for n := from; n < count; n++ {
		number := first + n
		// The last segment of a Period stops where the Period does.
		span := min(d, periodEnd-offset-n*d)
		if out, err = add(out, number, offset+n*d, span, a.place(number, number+1)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

var identifier = regexp.MustCompile(`\$(RepresentationID|Number|Time|Bandwidth|)(%0?\d*d)?\$`)

// expand fills a template's identifiers for one segment.
func (a addressed) expand(template string, number, at int64) string {
	return identifier.ReplaceAllStringFunc(template, func(m string) string {
		parts := identifier.FindStringSubmatch(m)
		format := cmp.Or(parts[2], "%d")
		switch parts[1] {
		case "":
			return "$"
		case "RepresentationID":
			return a.rep.Id
		case "Number":
			return fmt.Sprintf(format, number)
		case "Time":
			return fmt.Sprintf(format, at)
		default:
			return fmt.Sprintf(format, a.rep.Bandwidth)
		}
	})
}
