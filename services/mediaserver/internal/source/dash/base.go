package dash

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/Eyevinn/dash-mpd/mpd"

	"github.com/stupside/castor/services/mediaserver/internal/source/index"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// segmentBase is the SegmentBase the representation reads, each attribute from the nearest level that states it.
func (a addressed) segmentBase() *mpd.SegmentBaseType {
	return inherit(func(out, b *mpd.SegmentBaseType) {
		out.IndexRange = cmp.Or(out.IndexRange, b.IndexRange)
		out.Initialization = cmp.Or(out.Initialization, b.Initialization)
	}, a.rep.SegmentBase, a.set.SegmentBase, a.period.SegmentBase)
}

func (a addressed) indexed(b *mpd.SegmentBaseType, fetch indexer) ([]timeline.Segment, error) {
	at := byteRange(b.IndexRange)
	if at.Length == 0 {
		return nil, fmt.Errorf("representation %q states no index range", a.rep.Id)
	}
	webm := strings.Contains(strings.ToLower(cmp.Or(a.rep.MimeType, a.set.MimeType)), "webm")
	init, err := a.initialization(b.Initialization)
	switch {
	case err != nil:
		return nil, err
	case init == nil && webm:
		return nil, fmt.Errorf("representation %q names no init section to read its Cues against", a.rep.Id)
	case init == nil:
		// An ISO file keeps its init section ahead of its index.
		init = &timeline.Map{URI: a.base.String(), Range: timeline.Range{Offset: 0, Length: at.Offset}}
	}
	sidx, err := fetch(a.base.String(), at)
	if err != nil {
		return nil, fmt.Errorf("reading the segment index: %w", err)
	}
	var references []index.Reference
	var timescale int64
	if webm {
		// A WebM file indexes its clusters in Cues, whose positions count from the Segment its init section opens.
		head, err := fetch(init.URI, init.Range)
		if err != nil {
			return nil, fmt.Errorf("reading the init section: %w", err)
		}
		references, timescale, err = index.Cues(head, sidx, at.Offset)
		if err != nil {
			return nil, err
		}
	} else if references, timescale, err = index.Sidx(sidx, at.Offset); err != nil {
		return nil, err
	}
	out := make([]timeline.Segment, len(references))
	var before time.Duration
	for i, r := range references {
		out[i] = timeline.Segment{
			URI: a.base.String(), Range: timeline.Range{Offset: r.Offset, Length: r.Length}, Duration: ticks(r.Duration, timescale), Map: init,
			Place: a.place(int64(i), int64(i+1)),
		}
		// An index that could not tell its last subsegment's length leaves it to end where the Period does.
		if i == len(references)-1 && out[i].Duration == 0 {
			out[i].Duration = max(a.period.duration-before, 0)
		}
		before += out[i].Duration
	}
	return out, nil
}
