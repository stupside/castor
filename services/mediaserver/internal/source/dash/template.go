package dash

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
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

// templating is what the two ways a SegmentTemplate names its segments share: its clock, where its numbers begin, and how one segment is addressed.
type templating struct {
	addressed
	t         *mpd.SegmentTemplateType
	e         edge
	timescale int64
	first     int64
	offset    int64
	periodEnd int64
	init      *timeline.Map
}

func (a addressed) templated(t *mpd.SegmentTemplateType, e edge) ([]timeline.Segment, error) {
	for _, template := range []string{t.Media, t.Initialization} {
		if len(template)+len(a.rep.Id) > 4096 {
			return nil, fmt.Errorf("DASH segment template exceeds URL budget")
		}
		for _, m := range identifier.FindAllStringSubmatch(template, -1) {
			if m[2] != "" {
				width, err := strconv.Atoi(cmp.Or(strings.TrimSuffix(strings.TrimPrefix(m[2], "%"), "d"), "0"))
				if err != nil || width > 64 {
					return nil, fmt.Errorf("DASH segment template exceeds numeric width budget")
				}
			}
		}
	}
	timescale, first, offset := value(t.Timescale, 1), value(t.StartNumber, 1), value(t.PresentationTimeOffset, 0)
	if first < 0 || offset < 0 || offset > math.MaxInt64/4 {
		return nil, fmt.Errorf("DASH numeric field exceeds timeline range")
	}
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
	x := templating{addressed: a, t: t, e: e, timescale: timescale, first: first, offset: offset, periodEnd: math.MaxInt64, init: init}
	if e.live {
		for _, elapsed := range []time.Duration{a.elapsed(e), e.depth} {
			if math.Abs(elapsed.Seconds()*float64(timescale)) > float64(math.MaxInt64/4) {
				return nil, fmt.Errorf("DASH live clock exceeds timeline range")
			}
		}
	}
	if a.period.duration > 0 {
		if a.period.duration.Seconds()*float64(timescale) > float64(math.MaxInt64/4) {
			return nil, fmt.Errorf("DASH period exceeds timeline range")
		}
		x.periodEnd = offset + x.tick(a.period.duration)
	}
	if t.SegmentTimeline != nil {
		return x.timelined()
	}
	return x.numbered()
}

// add is out with the segment number that starts at at and lasts d ticks.
func (x templating) add(out []timeline.Segment, number, at, d int64, place timeline.Place) ([]timeline.Segment, error) {
	if err := x.budget.take(1); err != nil {
		return nil, err
	}
	u, err := x.base.Parse(x.expand(x.t.Media, number, at))
	if err != nil {
		return nil, err
	}
	return append(out, timeline.Segment{URI: u.String(), Duration: ticks(d, x.timescale), Map: x.init, Place: place}), nil
}

func (x templating) tick(d time.Duration) int64 { return int64(d.Seconds() * float64(x.timescale)) }

// timelined is the segments a SegmentTimeline lists.
func (x templating) timelined() ([]timeline.Segment, error) {
	var out []timeline.Segment
	var err error
	// Live, a segment exists once it has been produced, and stays only as long as the time-shift buffer keeps it.
	kept := int64(math.MinInt64)
	if x.e.live {
		kept = x.offset + x.tick(x.elapsed(x.e)-x.e.depth)
	}
	number, at := x.first, int64(0)
	entries := x.t.SegmentTimeline.S
	for i, s := range entries {
		if err := x.budget.check(0); err != nil {
			return nil, err
		}
		if s.T != nil {
			if *s.T > math.MaxInt64/4 {
				return nil, fmt.Errorf("DASH timestamp exceeds timeline range")
			}
			at = int64(*s.T)
		}
		d := int64(s.D)
		if d <= 0 || d > math.MaxInt64/4 {
			return nil, fmt.Errorf("representation %q lists a segment of duration %d", x.rep.Id, d)
		}
		if i+1 < len(entries) && entries[i+1].T != nil && *entries[i+1].T > math.MaxInt64/4 {
			return nil, fmt.Errorf("DASH timestamp exceeds timeline range")
		}
		repeats := int64(s.R)
		if repeats < 0 {
			// An open repeat runs to the next entry, or to the Period's end, or live, to the last segment produced.
			switch {
			case i+1 < len(entries) && entries[i+1].T != nil:
				repeats = max(0, (int64(*entries[i+1].T)-at+d-1)/d-1)
			case x.e.live:
				produced := min(x.periodEnd, x.offset+x.tick(x.elapsed(x.e)+early(x.t, ticks(d, x.timescale))))
				repeats = max(-1, (produced-at)/d-1)
			case x.periodEnd != math.MaxInt64:
				repeats = max(0, (x.periodEnd-at+d-1)/d-1)
			default:
				return nil, fmt.Errorf("representation %q repeats a segment without end", x.rep.Id)
			}
		}
		// Skip expired repeats arithmetically: a long-running timeline must not walk its entire history at every reload.
		if repeats == math.MaxInt64 {
			return nil, errExpansionLimit
		}
		count := repeats + 1
		if at < 0 || count > (math.MaxInt64-at)/d || number < 0 || count > math.MaxInt64-number {
			return nil, fmt.Errorf("DASH timeline arithmetic overflow")
		}
		skip := int64(0)
		if kept >= at+d {
			skip = min(count, (kept-at)/d)
		}
		until := count
		if x.periodEnd != math.MaxInt64 {
			until = min(until, max(0, (x.periodEnd-at+d-1)/d))
		}
		if x.e.live {
			produced := x.offset + x.tick(x.elapsed(x.e)+early(x.t, ticks(d, x.timescale)))
			until = min(until, max(0, (produced-at)/d))
		}
		if err := x.budget.check(max(0, until-skip)); err != nil {
			return nil, err
		}
		for n := skip; n < until; n++ {
			start := at + n*d
			if out, err = x.add(out, number+n, start, d, x.place(start, start+d)); err != nil {
				return nil, err
			}
		}
		number, at = number+count, at+count*d
	}
	return out, nil
}

// numbered is the segments a SegmentTemplate names by number, each lasting its duration.
func (x templating) numbered() ([]timeline.Segment, error) {
	var out []timeline.Segment
	var err error
	d := value(x.t.Duration, 0)
	length := ticks(d, x.timescale)
	if d <= 0 || length <= 0 {
		return nil, fmt.Errorf("representation %q names segments by neither a timeline nor a duration", x.rep.Id)
	}
	from, count := int64(0), int64(math.MaxInt64)
	if x.period.duration > 0 {
		count = int64(math.Ceil(float64(x.period.duration) / float64(length)))
	}
	if x.e.live {
		elapsed := x.elapsed(x.e)
		count = min(count, int64((elapsed+early(x.t, length))/length))
		from = max(0, int64((elapsed-x.e.depth)/length))
	}
	if count == math.MaxInt64 {
		return nil, fmt.Errorf("representation %q numbers segments without end", x.rep.Id)
	}
	if err := x.budget.check(max(0, count-from)); err != nil {
		return nil, err
	}
	if x.offset < 0 || x.first < 0 || count > (math.MaxInt64-x.offset)/d || count > math.MaxInt64-x.first {
		return nil, fmt.Errorf("DASH duration arithmetic overflow")
	}
	for n := from; n < count; n++ {
		number := x.first + n
		// The last segment of a Period stops where the Period does.
		span := min(d, x.periodEnd-x.offset-n*d)
		if out, err = x.add(out, number, x.offset+n*d, span, x.place(number, number+1)); err != nil {
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
