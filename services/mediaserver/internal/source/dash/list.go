package dash

import (
	"cmp"
	"fmt"

	"github.com/Eyevinn/dash-mpd/mpd"

	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// list is the SegmentList the representation reads, each attribute from the nearest level that states it.
func (a addressed) list() *mpd.SegmentListType {
	return inherit(func(out, l *mpd.SegmentListType) {
		out.Timescale = cmp.Or(out.Timescale, l.Timescale)
		out.Duration = cmp.Or(out.Duration, l.Duration)
		out.StartNumber = cmp.Or(out.StartNumber, l.StartNumber)
		out.Initialization = cmp.Or(out.Initialization, l.Initialization)
		out.SegmentTimeline = cmp.Or(out.SegmentTimeline, l.SegmentTimeline)
		if len(out.SegmentURL) == 0 {
			out.SegmentURL = l.SegmentURL
		}
	}, a.rep.SegmentList, a.set.SegmentList, a.period.SegmentList)
}

func (a addressed) listed(l *mpd.SegmentListType) ([]timeline.Segment, error) {
	timescale, first := value(l.Timescale, 1), value(l.StartNumber, 1)
	if timescale <= 0 {
		return nil, fmt.Errorf("representation %q states timescale %d", a.rep.Id, timescale)
	}
	init, err := a.initialization(l.Initialization)
	if err != nil {
		return nil, err
	}
	var durations []int64
	if l.SegmentTimeline != nil {
		for _, s := range l.SegmentTimeline.S {
			repeats := s.R
			// An open repeat runs to the last URL listed.
			if repeats < 0 {
				repeats = len(l.SegmentURL) - len(durations) - 1
			}
			for range repeats + 1 {
				durations = append(durations, int64(s.D))
			}
		}
	}
	out := make([]timeline.Segment, 0, len(l.SegmentURL))
	for i, s := range l.SegmentURL {
		u, err := a.base.Parse(string(s.Media))
		if err != nil {
			return nil, err
		}
		duration := ticks(value(l.Duration, 0), timescale)
		if i < len(durations) {
			duration = ticks(durations[i], timescale)
		}
		// One URL naming no duration is the whole Period.
		if duration == 0 && len(l.SegmentURL) == 1 {
			duration = a.period.duration
		}
		number := first + int64(i)
		out = append(out, timeline.Segment{
			URI: u.String(), Range: byteRange(string(s.MediaRange)), Duration: duration, Map: init,
			Place: a.place(number, number+1),
		})
	}
	return out, nil
}
