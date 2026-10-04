package follow

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/sourcetest"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// shape is a format whose inputs follow timeline, or are read directly when it is nil.
type shape struct{ timeline timeline.Source }

func (shape) Identity() source.Identity { return source.Identity{ContentType: media.HLS} }
func (shape) Recognize(string) bool     { return false }
func (shape) InputArgs(int) []string    { return nil }
func (s shape) Timeline(source.Client, media.Input, media.TrackKind) timeline.Source {
	return s.timeline
}
func (shape) Resolve(context.Context, source.Env, source.Subject) (source.Resolution, error) {
	return source.Resolution{}, nil
}

func republished(t *testing.T, s shape) *url.URL {
	t.Helper()
	p, err := media.NewProgram(media.Program{
		Inputs:     []media.Input{{ID: media.PrimaryInputID, URL: sourcetest.URL(t, "https://cdn.example/live/index.m3u8"), ContentType: media.HLS}},
		Tracks:     []media.TrackRef{{Input: media.PrimaryInputID, Kind: media.TrackVideo}},
		ClockInput: media.PrimaryInputID,
		EndPolicy:  media.EndAtLongest,
	})
	if err != nil {
		t.Fatal(err)
	}
	followed, stop, err := New(&sourcetest.Document{}, source.Formats{s}, 5*time.Second, nil).Republish(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stop() })
	in, _ := followed.PrimaryInput()
	return in.URL
}

func TestAnInputWithNoTimelineIsReadDirectly(t *testing.T) {
	if got := republished(t, shape{}).String(); got != "https://cdn.example/live/index.m3u8" {
		t.Errorf("an input with no timeline reads %s, want the origin's own", got)
	}
}

func TestATimelineCastorCannotStartStaysFfmpegsToRead(t *testing.T) {
	refused := &scripted{replies: []reply{{err: &timeline.Failure{Status: http.StatusForbidden, Err: errors.New("expired")}}}}
	if got := republished(t, shape{timeline: refused}).String(); got != "https://cdn.example/live/index.m3u8" {
		t.Errorf("an unreadable timeline reads %s, want the origin's own", got)
	}
}
