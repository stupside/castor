package follow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/dash"
	"github.com/stupside/castor/services/mediaserver/internal/source/sourcetest"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// shape is a format whose inputs follow a timeline, or are read directly when it is nil.
type shape struct {
	timeline       timeline.Source
	contentType    string
	directFallback bool
}

func (s shape) Identity() source.Identity {
	if s.contentType != "" {
		return source.Identity{ContentType: s.contentType}
	}
	return source.Identity{ContentType: media.HLS}
}
func (shape) Recognize(string) bool  { return false }
func (shape) InputArgs(int) []string { return nil }
func (s shape) Timeline(source.Client, media.Input, media.TrackKind) timeline.Reading {
	return timeline.Reading{Source: s.timeline, Decoder: timeline.IdentityDecoder{}, DirectFallback: s.directFallback}
}
func (shape) Resolve(context.Context, source.Env, source.Subject) (source.Resolution, error) {
	return source.Resolution{}, nil
}

func TestAnUnselectedDASHPresentationUsesTheDirectReader(t *testing.T) {
	const address = "https://cdn.example/live/index.mpd"
	program, err := source.ProgramFor(&source.Stream{URL: sourcetest.URL(t, address), ContentType: media.DASH})
	if err != nil {
		t.Fatal(err)
	}
	followed, stop, err := New(&sourcetest.Document{}, source.Formats{dash.Format{}}, nil).Republish(t.Context(), program)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	in, _ := followed.PrimaryInput()
	if got := in.URL.String(); got != address {
		t.Errorf("unselected DASH input reads %s, want the origin's own", got)
	}
}

func TestAnHLSPlaylistCastorCannotFollowIsNotReadDirectly(t *testing.T) {
	refused := &scripted{replies: []reply{{err: &timeline.Failure{Status: http.StatusForbidden, Err: errors.New("expired")}}}}
	program, err := source.ProgramFor(&source.Stream{URL: sourcetest.URL(t, "https://cdn.example/live/index.m3u8"), ContentType: media.HLS})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := New(&sourcetest.Document{}, source.Formats{shape{timeline: refused}}, nil).Republish(t.Context(), program); err == nil {
		t.Fatal("unreadable HLS playlist fell back to direct FFmpeg access")
	}
}

func TestAFormatThatPermitsDirectFallbackUsesIt(t *testing.T) {
	refused := &scripted{replies: []reply{{err: &timeline.Failure{Status: http.StatusForbidden, Err: errors.New("expired")}}}}
	program, err := source.ProgramFor(&source.Stream{URL: sourcetest.URL(t, "https://cdn.example/live/index.mpd"), ContentType: media.DASH})
	if err != nil {
		t.Fatal(err)
	}
	followed, stop, err := New(&sourcetest.Document{}, source.Formats{shape{timeline: refused, contentType: media.DASH, directFallback: true}}, nil).Republish(t.Context(), program)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	input, _ := followed.PrimaryInput()
	if got := input.URL.String(); got != "https://cdn.example/live/index.mpd" {
		t.Errorf("unreadable timeline reads %s, want the origin's own", got)
	}
}

type missingDecoder struct{ shape }

func (s missingDecoder) Timeline(source.Client, media.Input, media.TrackKind) timeline.Reading {
	return timeline.Reading{Source: s.timeline}
}

func TestAFormatWithoutDecoderIsRejectedBeforeReading(t *testing.T) {
	program, err := source.ProgramFor(&source.Stream{URL: sourcetest.URL(t, "https://cdn.example/live/index.m3u8"), ContentType: media.HLS})
	if err != nil {
		t.Fatal(err)
	}
	stream := &scripted{replies: []reply{{window: listed("a", 0, 3)}}}
	_, _, err = New(&sourcetest.Document{}, source.Formats{missingDecoder{shape{timeline: stream}}}, nil).Republish(t.Context(), program)
	if err == nil || !strings.Contains(err.Error(), "no resource decoder") || stream.asked != 0 {
		t.Fatalf("missing decoder: error = %v, window reads = %d", err, stream.asked)
	}
}

type closingOrigin struct {
	started   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
}

func (o *closingOrigin) Window(context.Context) (timeline.Window, error) {
	return timeline.Window{Segments: []timeline.Segment{{URI: "https://origin.example/0.ts", Duration: time.Second}}, Closed: true}, nil
}
func (o *closingOrigin) Read(ctx context.Context, _ string, _ timeline.Range) (io.ReadCloser, error) {
	close(o.started)
	<-ctx.Done()
	close(o.cancelled)
	<-o.release
	return nil, context.Cause(ctx)
}

func TestRepublisherStopJoinsOriginCleanup(t *testing.T) {
	origin := &closingOrigin{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	program, err := media.NewProgram(media.Program{
		Inputs:     []media.Input{{ID: media.PrimaryInputID, URL: sourcetest.URL(t, "https://origin.example/index.m3u8"), ContentType: media.HLS}},
		Tracks:     []media.TrackRef{{Input: media.PrimaryInputID, Kind: media.TrackVideo}},
		ClockInput: media.PrimaryInputID, EndPolicy: media.EndAtLongest,
	})
	if err != nil {
		t.Fatal(err)
	}
	followed, stop, err := New(&sourcetest.Document{}, source.Formats{shape{timeline: origin}}, nil).Republish(t.Context(), program)
	if err != nil {
		t.Fatal(err)
	}
	release := sync.OnceFunc(func() { close(origin.release) })
	t.Cleanup(func() { release(); _ = stop() })
	input, _ := followed.PrimaryInput()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := http.Get(input.URL.ResolveReference(&url.URL{Path: "primary/0"}).String())
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	<-origin.started
	stopped := make(chan error, 1)
	go func() { stopped <- stop() }()
	<-origin.cancelled
	select {
	case err := <-stopped:
		t.Fatalf("stop returned before origin cleanup: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not join completed origin cleanup")
	}
	<-requestDone
}

type closingWindow struct{ closingOrigin }

func (o *closingWindow) Window(ctx context.Context) (timeline.Window, error) {
	close(o.started)
	<-ctx.Done()
	close(o.cancelled)
	<-o.release
	return timeline.Window{}, context.Cause(ctx)
}
func TestServerCloseJoinsPlaylistHandler(t *testing.T) {
	origin := &closingWindow{closingOrigin{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}}
	server := serving(t, origin, nil)
	release := sync.OnceFunc(func() { close(origin.release) })
	t.Cleanup(release)
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := http.Get(server.URL("primary").String())
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	<-origin.started
	stopped := make(chan error, 1)
	go func() { stopped <- server.Close() }()
	<-origin.cancelled
	select {
	case err := <-stopped:
		t.Fatalf("server returned before handler cleanup: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not join completed handler")
	}
	<-requestDone
}

func TestRepublisherRetainsPlaylistRefusalUntilRecovery(t *testing.T) {
	for _, status := range []int{401, 403, 404, 410} {
		for _, recover := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/recover=%t", status, recover), func(t *testing.T) {
				refused := &timeline.Failure{Status: status, Err: errors.New("playlist refused")}
				window := timeline.Window{Segments: []timeline.Segment{{URI: "https://origin.example/0.ts", Duration: time.Second}}}
				origin := &scripted{replies: []reply{{window: window}, {err: refused}, {err: errors.New("temporary failure")}, {window: window}}}
				program, err := media.NewProgram(media.Program{
					Inputs:     []media.Input{{ID: media.PrimaryInputID, URL: sourcetest.URL(t, "https://origin.example/index.m3u8"), ContentType: media.HLS}},
					Tracks:     []media.TrackRef{{Input: media.PrimaryInputID, Kind: media.TrackVideo}},
					ClockInput: media.PrimaryInputID, EndPolicy: media.EndAtLongest,
				})
				if err != nil {
					t.Fatal(err)
				}
				followed, stop, err := New(&sourcetest.Document{}, source.Formats{shape{timeline: origin}}, nil).Republish(t.Context(), program)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stop() })
				input, _ := followed.PrimaryInput()
				if got, _ := fetch(t, input.URL.String()); got != status {
					t.Fatalf("refusal status=%d", got)
				}
				// A transient failure cannot republish a playlist the origin refused.
				if got, _ := fetch(t, input.URL.String()); got != status {
					t.Fatalf("unresolved refusal status=%d", got)
				}
				if recover {
					if got, _ := fetch(t, input.URL.String()); got != http.StatusOK {
						t.Fatalf("recovery status=%d", got)
					}
				}
				err = stop()
				if recover && err != nil {
					t.Fatalf("recovered feed failed: %v", err)
				}
				if !recover && !errors.Is(err, refused) {
					t.Fatalf("lost playlist refusal: %v", err)
				}
			})
		}
	}
}

type inspectionFunc func(context.Context) (media.ProbeInfo, media.Reach, error)

func (f inspectionFunc) Probe(ctx context.Context) (media.ProbeInfo, media.Reach, error) {
	return f(ctx)
}

func TestInspectionCancellationJoinsOriginBeforeReturning(t *testing.T) {
	origin := &closingOrigin{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(origin.release) })
	t.Cleanup(release)
	program, err := source.ProgramFor(&source.Stream{URL: sourcetest.URL(t, "https://origin.example/index.m3u8"), ContentType: media.HLS})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := New(&sourcetest.Document{}, source.Formats{shape{timeline: origin}}, nil).Probe(ctx, program, func(p media.Program) media.Prober {
			return inspectionFunc(func(ctx context.Context) (media.ProbeInfo, media.Reach, error) {
				in, _ := p.PrimaryInput()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL.ResolveReference(&url.URL{Path: "primary/0"}).String(), nil)
				if err != nil {
					return media.ProbeInfo{}, media.ReachUnproven, err
				}
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					_ = resp.Body.Close()
				}
				// The loopback response can beat cancellation to the inspector. Make
				// that outcome deterministic while still exercising the origin join.
				if ctx.Err() != nil {
					err = nil
				}
				return media.ProbeInfo{}, media.ReachUnproven, err
			})
		})
		done <- err
	}()
	select {
	case <-origin.started:
	case <-time.After(time.Second):
		t.Fatal("inspection did not start reading")
	}
	cancel()
	select {
	case <-origin.cancelled:
	case <-time.After(time.Second):
		t.Fatal("origin was not cancelled")
	}
	select {
	case err := <-done:
		t.Fatalf("inspection returned before origin cleanup: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("inspection failed to join the released origin")
	}
}
