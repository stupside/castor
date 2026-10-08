package dash

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Eyevinn/dash-mpd/mpd"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// Timeline translates the one representation a DASH input reads into the segments castor republishes.
func (Format) Timeline(c source.Client, in media.Input, reads media.TrackKind) timeline.Source {
	if in.Representation == "" {
		return nil
	}
	return &follower{
		Client:  c,
		Headers: in.Headers,
		url:     in.URL, kind: reads, id: in.Representation,
	}
}

// follower reads the presentation afresh each window, keeping what it learned once: the track it matches and the clock.
type follower struct {
	source.Upstream
	kind media.TrackKind
	id   string

	url    *url.URL
	want   *wanted
	offset *time.Duration
}

// defaultDepth is how far back castor lists a live presentation that states no time-shift buffer: a reader joins three segments from the edge.
const defaultDepth = 30 * time.Second

func (f *follower) Window(ctx context.Context) (timeline.Window, error) {
	body, from, status, err := f.Client.Fetch(ctx, f.url, f.Headers)
	if err != nil {
		return timeline.Window{}, &timeline.Failure{Status: status, Err: err}
	}
	m, ok := parse(body)
	if !ok {
		return timeline.Window{}, fmt.Errorf("%s is no longer a DASH presentation", f.url.Redacted())
	}
	// A presentation that moves names its next address itself.
	if len(m.Location) > 0 {
		if moved, err := from.Parse(strings.TrimSpace(m.Location[0].Value)); err == nil {
			f.url = moved
		}
	}
	periods := m.placed()
	if f.want == nil {
		w, found := wantedIn(periods, feature(periods, m.live()), f.kind, f.id)
		if !found {
			return timeline.Window{}, fmt.Errorf("the presentation offers no %s representation %q", f.kind, f.id)
		}
		f.want = &w
	}
	e := edge{live: m.live()}
	if e.live {
		if e.available = instant(m.AvailabilityStartTime); e.available.IsZero() {
			return timeline.Window{}, errNoEdge
		}
		e.now = time.Now().Add(f.skew(ctx, m, from))
		e.depth = span(m.TimeShiftBufferDepth)
		if e.depth == 0 {
			e.depth = defaultDepth
		}
	}
	fetch := func(uri string, r timeline.Range) ([]byte, error) {
		body, err := f.Read(ctx, uri, r)
		if err != nil {
			return nil, err
		}
		defer func() { _ = body.Close() }()
		return source.ReadDocument(body)
	}
	var w timeline.Window
	for _, p := range periods {
		o, offers := f.want.in(p.Period)
		if !offers {
			continue
		}
		segments, err := m.addressed(from, p, o).segments(e, fetch)
		if err != nil {
			return timeline.Window{}, err
		}
		w.Segments = append(w.Segments, segments...)
	}
	w.Closed = !e.live
	return w, nil
}

// skew is how far the presentation's clock stands from this machine's, read from UTCTiming until one answers; zero when it names none castor reads.
func (f *follower) skew(ctx context.Context, m presentation, from *url.URL) time.Duration {
	if f.offset != nil {
		return *f.offset
	}
	asked := false
	for _, t := range m.UTCTimings {
		var stated time.Time
		switch scheme := string(t.SchemeIdUri); {
		case strings.HasSuffix(scheme, ":direct:2014"):
			stated = instant(mpd.DateTime(t.Value))
		case strings.HasSuffix(scheme, ":http-iso:2014"), strings.HasSuffix(scheme, ":http-xsdate:2014"):
			asked = true
			// The value may list several servers; any one of them tells the time.
			for server := range strings.FieldsSeq(t.Value) {
				u, err := from.Parse(server)
				if err != nil {
					continue
				}
				if body, _, _, err := f.Client.Fetch(ctx, u, nil); err == nil {
					stated = instant(mpd.DateTime(body))
					break
				}
			}
		}
		if !stated.IsZero() {
			f.offset = new(time.Until(stated))
			return *f.offset
		}
	}
	// A clock server that did not answer is asked again at the next window.
	if !asked {
		f.offset = new(time.Duration)
	}
	return 0
}
