package apiserver_test

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"google.golang.org/protobuf/proto"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/gen/castor/media/v1/mediav1connect"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/gen/castor/v1/castorv1connect"
	"github.com/stupside/castor/services/apiserver"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/mediaclient"
)

// family is the one device on the network: each connection to it plays what it is handed until its cast lets it go.
type family struct {
	played chan string
	closed chan int
	// unreachable is a device that answers no connection.
	unreachable bool
	// goneFirst is a device whose first connection is found gone at its first play.
	goneFirst bool

	mu        sync.Mutex
	connected int
}

func newFamily() *family { return &family{played: make(chan string, 4), closed: make(chan int, 4)} }

var bedroom = device.Info{ID: "uuid-1", Name: "Bedroom", Type: "dlna", Address: "10.0.0.9"}

func (*family) Type() device.Type { return bedroom.Type }

func (*family) Discover(context.Context) []device.Info { return []device.Info{bedroom} }

func (*family) Locate(_ context.Context, address string) (string, error) { return address, nil }

func (f *family) Connect(context.Context, device.Info) (device.Device, error) {
	if f.unreachable {
		return nil, errors.New("no answer from the device")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected++
	return &connection{f: f, n: f.connected, gone: f.goneFirst && f.connected == 1}, nil
}

func (f *family) connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

// connection is one connection to the device, numbered from 1.
type connection struct {
	f    *family
	n    int
	gone bool
}

func (c *connection) Play(_ context.Context, u *url.URL, _ mediav1.Container) error {
	if c.gone {
		return &device.Gone{Device: "Bedroom", Observed: "the connection closed"}
	}
	c.f.played <- u.String()
	return nil
}

func (*connection) AwaitEnd(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (*connection) Capabilities() *mediav1.Capabilities { return &mediav1.Capabilities{} }

func (c *connection) Close() error {
	c.f.closed <- c.n
	return nil
}

// media is the media server at its contract: a cast plays its best stream on the lent device, then ends unless held, until it is stopped.
type media struct {
	held bool
	// searching is a media server that never finds a stream on a cast's pages.
	searching bool
	// asked is every cast's and ranking's preferences, as the media server was asked them.
	asked chan *castorv1.Preferences
	// stopped closes once a stop reaches a cast.
	stopped chan struct{}
	stop    func()

	mu    sync.Mutex
	casts map[string]*mediaCast
}

func newMedia(held bool) *media {
	stopped := make(chan struct{})
	return &media{held: held, asked: make(chan *castorv1.Preferences, 8), stopped: stopped, stop: sync.OnceFunc(func() { close(stopped) }), casts: map[string]*mediaCast{}}
}

// ranked is the media server's ranking of source: a stream as is, the two streams it finds on any pages best last.
func ranked(source *castorv1.Source) []*castorv1.RankedStream {
	if stream := source.GetStream(); stream != nil {
		return []*castorv1.RankedStream{{Url: stream.GetUrl()}}
	}
	return []*castorv1.RankedStream{{Url: "https://cdn.example/b.m3u8"}, {Url: "https://cdn.example/a.m3u8"}}
}

func (m *media) Rank(_ context.Context, req *mediav1.RankRequest) (*mediav1.RankResponse, error) {
	m.asked <- req.GetPreferences()
	return &mediav1.RankResponse{Ranked: ranked(req.GetSource())}, nil
}

func (m *media) Start(_ context.Context, req *mediav1.StartRequest) (*mediav1.StartResponse, error) {
	m.asked <- req.GetPreferences()
	c := &mediaCast{source: req.GetSource(), answers: make(chan *mediav1.AnswerRequest, 1), more: make(chan struct{}), done: make(chan struct{})}
	phase := castorv1.Phase_PHASE_MEASURING
	if req.GetSource().GetPages() != nil {
		phase = castorv1.Phase_PHASE_EXTRACTING
	}
	c.status(&castorv1.CastStatus{Phase: phase})
	c.line("engine at work")
	m.mu.Lock()
	defer m.mu.Unlock()
	id := rand.Text()
	m.casts[id] = c
	return &mediav1.StartResponse{CastId: id}, nil
}

func (m *media) find(id string) (*mediaCast, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.casts[id]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no cast %q", id))
	}
	return c, nil
}

// Stop ends the cast stopped, logging as it lets go more lines than a watch takes in at once.
func (m *media) Stop(_ context.Context, req *mediav1.StopRequest) (*mediav1.StopResponse, error) {
	c, err := m.find(req.GetCastId())
	if err != nil {
		return nil, err
	}
	m.stop()
	for i := range 200 {
		c.line(fmt.Sprintf("engine letting go %d", i))
	}
	c.line("engine let go")
	c.end(&castorv1.Ended{Outcome: castorv1.Outcome_OUTCOME_STOPPED})
	return &mediav1.StopResponse{}, nil
}

func (m *media) Watch(ctx context.Context, req *castorv1.WatchRequest, out *connect.ServerStream[castorv1.WatchResponse]) error {
	c, err := m.find(req.GetCastId())
	if err != nil {
		return err
	}
	for next := 0; ; {
		updates, more := c.since(next)
		next += len(updates)
		for _, u := range updates {
			if u.GetLine() != nil && req.Logs == nil {
				continue
			}
			if err := out.Send(u); err != nil {
				return err
			}
			if u.GetEnded() != nil {
				return nil
			}
		}
		select {
		case <-more:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Drive plays the cast's best stream on the lent device, unless its pages hold none, and leads it until the cast ends.
func (m *media) Drive(ctx context.Context, req *mediav1.DriveRequest, out *connect.ServerStream[mediav1.DriveResponse]) error {
	c, err := m.find(req.GetCastId())
	if err != nil {
		return err
	}
	if !m.searching || c.source.GetPages() == nil {
		play := &mediav1.DeviceCommand_Play{Url: ranked(c.source)[0].GetUrl(), Container: mediav1.Container_CONTAINER_HLS}
		if err := out.Send(&mediav1.DriveResponse{Command: &mediav1.DeviceCommand{Id: "play", Command: &mediav1.DeviceCommand_Play_{Play: play}}}); err != nil {
			return err
		}
		select {
		case a := <-c.answers:
			if e := a.GetError(); e != nil {
				c.end(&castorv1.Ended{Outcome: castorv1.Outcome_OUTCOME_FAILED, Reason: e.GetMessage()})
				break
			}
			c.status(&castorv1.CastStatus{Phase: castorv1.Phase_PHASE_CASTING, Attempt: 1})
			if !m.held {
				c.end(&castorv1.Ended{Outcome: castorv1.Outcome_OUTCOME_ENDED})
			}
		case <-c.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *media) Answer(_ context.Context, req *mediav1.AnswerRequest) (*mediav1.AnswerResponse, error) {
	c, err := m.find(req.GetCastId())
	if err != nil {
		return nil, err
	}
	select {
	case c.answers <- req:
		return &mediav1.AnswerResponse{}, nil
	default:
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no device command %q awaits an answer", req.GetCommandId()))
	}
}

// mediaCast is one cast on the media server: everything its watchers are sent, in order, its end last.
type mediaCast struct {
	source  *castorv1.Source
	answers chan *mediav1.AnswerRequest
	done    chan struct{}

	mu      sync.Mutex
	updates []*castorv1.WatchResponse
	more    chan struct{}
}

func (c *mediaCast) publish(u *castorv1.WatchResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
		return
	default:
	}
	c.updates = append(c.updates, u)
	if u.GetEnded() != nil {
		close(c.done)
	}
	close(c.more)
	c.more = make(chan struct{})
}

func (c *mediaCast) since(next int) ([]*castorv1.WatchResponse, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.updates[next:]), c.more
}

func (c *mediaCast) status(s *castorv1.CastStatus) {
	c.publish(&castorv1.WatchResponse{Update: &castorv1.WatchResponse_Status{Status: s}})
}

func (c *mediaCast) line(message string) {
	c.publish(&castorv1.WatchResponse{Update: &castorv1.WatchResponse_Line{Line: &castorv1.LogLine{Level: castorv1.LogLevel_LOG_LEVEL_INFO, Message: message}}})
}

func (c *mediaCast) end(e *castorv1.Ended) {
	c.publish(&castorv1.WatchResponse{Update: &castorv1.WatchResponse_Ended{Ended: e}})
}

type api struct {
	casts   castorv1connect.CastServiceClient
	devices castorv1connect.DeviceServiceClient
	server  *apiserver.Server
}

// setup is what a test runs its servers with; guard, when set, stands in front of the media server.
type setup struct {
	media  *media
	family *family
	guard  func(http.Handler) http.Handler
	// cast is the cast section the API server runs on; unset, the one castor ships.
	cast apiserver.CastConfig
}

var shipped = apiserver.CastConfig{Delivery: "auto", MaxHeight: 1080}

// serve runs the media server's contract and the API server before it, as castor does, and returns a client of the API.
func serve(t *testing.T, s setup) api {
	t.Helper()
	// Both ways, every message is held to the rules the contract states, as the media server holds them.
	valid := connect.WithInterceptors(validate.NewInterceptor(validate.WithValidateResponses()))
	mux := http.NewServeMux()
	mux.Handle(mediav1connect.NewCastServiceHandler(s.media, valid))
	mux.Handle(mediav1connect.NewDeviceServiceHandler(s.media, valid))
	mux.Handle(mediav1connect.NewStreamServiceHandler(s.media, valid))
	var handler http.Handler = mux
	if s.guard != nil {
		handler = s.guard(handler)
	}
	mediaAPI := httptest.NewTestServer(t, handler)
	srv, err := apiserver.New(apiserver.Backend{
		Devices: device.Registry{Families: []device.Family{s.family}, Timeout: time.Second},
		Media:   mediaclient.New(mediaAPI.Client(), mediaAPI.URL),
	}, cmp.Or(s.cast, shipped))
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewTestServer(t, srv)
	// Registered last, so it runs before either server closes.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	})
	return api{
		casts:   castorv1connect.NewCastServiceClient(public.Client(), public.URL),
		devices: castorv1connect.NewDeviceServiceClient(public.Client(), public.URL),
		server:  srv,
	}
}

func pagesOf(urls ...string) *castorv1.Source {
	return &castorv1.Source{Source: &castorv1.Source_Pages_{Pages: &castorv1.Source_Pages{Urls: urls}}}
}

func streamOf(raw string) *castorv1.Source {
	return &castorv1.Source{Source: &castorv1.Source_Stream{Stream: &castorv1.Stream{Url: raw}}}
}

var onBedroom = &castorv1.Target{Target: &castorv1.Target_DeviceId{DeviceId: "dlna:uuid-1"}}

func cast(t *testing.T, c api, source *castorv1.Source) string {
	t.Helper()
	started, err := c.casts.Cast(t.Context(), &castorv1.CastRequest{Target: onBedroom, Source: source})
	if err != nil {
		t.Fatal(err)
	}
	return started.GetCastId()
}

// watched is what a watcher was sent of one cast.
type watched struct {
	shown []*castorv1.CastStatus
	lines []*castorv1.LogLine
	ended *castorv1.Ended
}

// watch follows cast id to its end, from logs on when set.
func watch(t *testing.T, c api, id string, logs *castorv1.LogLevel) watched {
	t.Helper()
	stream, err := c.casts.Watch(t.Context(), &castorv1.WatchRequest{CastId: id, Logs: logs})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var w watched
	for stream.Receive() {
		switch u := stream.Msg().GetUpdate().(type) {
		case *castorv1.WatchResponse_Status:
			w.shown = append(w.shown, u.Status)
		case *castorv1.WatchResponse_Line:
			w.lines = append(w.lines, u.Line)
		case *castorv1.WatchResponse_Ended:
			w.ended = u.Ended
			return w
		}
	}
	t.Fatalf("the watch ended without saying how the cast did: %v", stream.Err())
	return w
}

// forward fails t if a status shown went back on the one before it.
func forward(t *testing.T, shown []*castorv1.CastStatus) {
	t.Helper()
	for i := 1; i < len(shown); i++ {
		if shown[i].GetPhase() < shown[i-1].GetPhase() {
			t.Errorf("the cast went back from %v to %v", shown[i-1].GetPhase(), shown[i].GetPhase())
		}
	}
}

func TestADeviceListedIsCastOnAndTheCastEndsShowingItsLastAttempt(t *testing.T) {
	f := newFamily()
	c := serve(t, setup{media: newMedia(false), family: f})

	listed, err := c.devices.ListDevices(t.Context(), &castorv1.ListDevicesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	devices := listed.GetDevices()
	if len(devices) != 1 || devices[0].GetId() != "dlna:uuid-1" || devices[0].GetName() != "Bedroom" {
		t.Fatalf("listed %v, want the bedroom device under its family and identity", devices)
	}
	w := watch(t, c, cast(t, c, streamOf("https://cdn.example/direct")), nil)
	if w.ended.GetOutcome() != castorv1.Outcome_OUTCOME_ENDED {
		t.Errorf("the cast ended %v, want ended", w.ended)
	}
	forward(t, w.shown)
	for _, s := range w.shown {
		if s.GetPhase() == castorv1.Phase_PHASE_EXTRACTING {
			t.Errorf("a cast of a stream showed %v", s)
		}
	}
	if last := w.shown[len(w.shown)-1]; last.GetPhase() != castorv1.Phase_PHASE_CASTING || last.GetAttempt() != 1 {
		t.Errorf("the watcher was last shown %v, want the first attempt casting", last)
	}
	if got := <-f.played; got != "https://cdn.example/direct" {
		t.Errorf("the device played %q, want the stream as is", got)
	}
}

func TestPagesAreSearchedThenTheirBestStreamIsCast(t *testing.T) {
	f := newFamily()
	c := serve(t, setup{media: newMedia(false), family: f})

	resolved, err := c.casts.Resolve(t.Context(), &castorv1.ResolveRequest{Source: pagesOf("https://site.example/watch")})
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.GetRanked(); len(got) != 2 || got[0].GetUrl() != "https://cdn.example/b.m3u8" {
		t.Errorf("resolved %v, want the page's streams in the media server's order", got)
	}

	started, err := c.casts.Cast(t.Context(), &castorv1.CastRequest{
		Target: &castorv1.Target{Target: &castorv1.Target_Pinned_{Pinned: &castorv1.Target_Pinned{Type: castorv1.DeviceType_DEVICE_TYPE_DLNA, Address: "10.0.0.9"}}},
		Source: pagesOf("https://site.example/watch"),
	})
	if err != nil {
		t.Fatal(err)
	}
	w := watch(t, c, started.GetCastId(), nil)
	if w.ended.GetOutcome() != castorv1.Outcome_OUTCOME_ENDED {
		t.Errorf("the cast ended %v, want ended", w.ended)
	}
	forward(t, w.shown)
	if got := <-f.played; got != "https://cdn.example/b.m3u8" {
		t.Errorf("the device played %q, want the ranking's head", got)
	}
}

func TestRequestPreferencesOverrideTheDefaultsFieldByField(t *testing.T) {
	m := newMedia(false)
	c := serve(t, setup{media: m, family: newFamily()})

	for _, tc := range []struct {
		asked, want *castorv1.Preferences
	}{
		{nil, &castorv1.Preferences{Delivery: castorv1.Delivery_DELIVERY_AUTO.Enum(), MaxHeight: new(uint32(1080)), Subtitles: new("")}},
		{&castorv1.Preferences{MaxHeight: new(uint32(720))}, &castorv1.Preferences{Delivery: castorv1.Delivery_DELIVERY_AUTO.Enum(), MaxHeight: new(uint32(720)), Subtitles: new("")}},
		{
			&castorv1.Preferences{Delivery: castorv1.Delivery_DELIVERY_SERVE.Enum(), Subtitles: new("fr")},
			&castorv1.Preferences{Delivery: castorv1.Delivery_DELIVERY_SERVE.Enum(), MaxHeight: new(uint32(1080)), Subtitles: new("fr")},
		},
	} {
		if _, err := c.casts.Resolve(t.Context(), &castorv1.ResolveRequest{Source: streamOf("https://cdn.example/direct"), Preferences: tc.asked}); err != nil {
			t.Fatal(err)
		}
		if got := <-m.asked; !proto.Equal(got, tc.want) {
			t.Errorf("asking %v resolved as %v, want %v", tc.asked, got, tc.want)
		}
	}
}

func TestEveryCastAsksWhatTheCastSectionSaysAndNoSubtitlesUnlessItNamesALanguage(t *testing.T) {
	m := newMedia(false)
	c := serve(t, setup{media: m, family: newFamily(), cast: apiserver.CastConfig{Delivery: "serve", MaxHeight: 720}})

	if _, err := c.casts.Resolve(t.Context(), &castorv1.ResolveRequest{Source: streamOf("https://cdn.example/direct")}); err != nil {
		t.Fatal(err)
	}
	want := &castorv1.Preferences{Delivery: castorv1.Delivery_DELIVERY_SERVE.Enum(), MaxHeight: new(uint32(720)), Subtitles: new("")}
	if got := <-m.asked; !proto.Equal(got, want) {
		t.Errorf("a cast asking nothing resolved as %v, want serve at 720p and no subtitles", got)
	}
}

func TestACastSectionTheContractRefusesIsRefusedBeforeTheFirstCast(t *testing.T) {
	for name, cast := range map[string]apiserver.CastConfig{
		"a delivery the contract does not name":     {Delivery: "fast", MaxHeight: 1080},
		"a picture no device shows":                 {Delivery: "auto", MaxHeight: 1},
		"a subtitle language whisper does not name": {Delivery: "auto", MaxHeight: 1080, Subtitles: "english"},
	} {
		if _, err := apiserver.New(apiserver.Backend{}, cast); err == nil {
			t.Errorf("%s was taken as every cast's default", name)
		}
	}
}

func TestAWatchAskingForLinesHasThemAllBeforeTheEnd(t *testing.T) {
	c := serve(t, setup{media: newMedia(true), family: newFamily()})

	id := cast(t, c, streamOf("https://cdn.example/direct"))
	stream, err := c.casts.Watch(t.Context(), &castorv1.WatchRequest{CastId: id, Logs: castorv1.LogLevel_LOG_LEVEL_INFO.Enum()})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var said []string
	for stream.Receive() {
		if line := stream.Msg().GetLine(); line != nil {
			if len(said) == 0 {
				if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: id}); err != nil {
					t.Fatal(err)
				}
			}
			said = append(said, line.GetMessage())
		}
		if stream.Msg().GetEnded() != nil {
			break
		}
	}
	if !slices.Contains(said, "engine let go") {
		t.Errorf("the watch ended having sent %q, want the engine's last line before the end", said)
	}
}

func TestALateWatcherLearnsWhereTheCastStoodAndHowItEnded(t *testing.T) {
	c := serve(t, setup{media: newMedia(false), family: newFamily()})

	id := cast(t, c, streamOf("https://cdn.example/direct"))
	first := watch(t, c, id, nil)
	late := watch(t, c, id, nil)
	if len(late.shown) != 1 || !proto.Equal(late.shown[0], first.shown[len(first.shown)-1]) || !proto.Equal(late.ended, first.ended) {
		t.Errorf("a watcher after the end was sent %v then %v, want the last status %v then %v", late.shown, late.ended, first.shown[len(first.shown)-1], first.ended)
	}
}

func TestACastNoOneStartedIsNotFound(t *testing.T) {
	c := serve(t, setup{media: newMedia(false), family: newFamily()})

	if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: "none"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("stopping an unknown cast was met with %v, want not found", err)
	}
	stream, err := c.casts.Watch(t.Context(), &castorv1.WatchRequest{CastId: "none"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if stream.Receive() || connect.CodeOf(stream.Err()) != connect.CodeNotFound {
		t.Errorf("watching an unknown cast was met with %v, want not found", stream.Err())
	}
	if _, err := c.casts.Cast(t.Context(), &castorv1.CastRequest{Target: &castorv1.Target{Target: &castorv1.Target_DeviceId{DeviceId: "dlna:nowhere"}}, Source: pagesOf("https://site.example/watch")}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("a device not on the network was met with %v, want not found", err)
	}
}

func TestAStoppedCastReleasesItsDeviceAndIsNoLongerListed(t *testing.T) {
	f := newFamily()
	c := serve(t, setup{media: newMedia(true), family: f})

	id := cast(t, c, streamOf("https://cdn.example/direct"))
	<-f.played
	if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: id}); err != nil {
		t.Fatal(err)
	}
	if w := watch(t, c, id, nil); w.ended.GetOutcome() != castorv1.Outcome_OUTCOME_STOPPED {
		t.Errorf("the cast ended %v, want stopped", w.ended)
	}
	select {
	case <-f.closed:
	case <-time.After(5 * time.Second):
		t.Error("the device was left open after its cast stopped")
	}
	if listed, _ := c.casts.ListCasts(t.Context(), &castorv1.ListCastsRequest{}); len(listed.GetCasts()) != 0 {
		t.Errorf("listed %v after the stop, want nothing playing", listed.GetCasts())
	}
}

func TestCastsAreListedOldestFirstWithoutTheHeadersTheirStreamsWereFoundWith(t *testing.T) {
	f := newFamily()
	c := serve(t, setup{media: newMedia(true), family: f})

	var ids []string
	for _, raw := range []string{"https://cdn.example/first", "https://cdn.example/second"} {
		source := streamOf(raw)
		source.GetStream().Headers = map[string]string{"Cookie": "session=viewer"}
		ids = append(ids, cast(t, c, source))
		<-f.played
	}
	listed, err := c.casts.ListCasts(t.Context(), &castorv1.ListCastsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	got := listed.GetCasts()
	if len(got) != 2 || got[0].GetId() != ids[0] || got[1].GetId() != ids[1] || got[0].GetDevice().GetName() != "Bedroom" {
		t.Fatalf("listed %v, want both casts on the bedroom device, oldest first", got)
	}
	for _, listed := range got {
		if h := listed.GetSource().GetStream().GetHeaders(); len(h) != 0 {
			t.Errorf("a listed cast showed the headers %v its stream was found with", h)
		}
	}
}

func TestACastStoppedWhileItsPagesAreSearchedEndsStoppedWithoutPlaying(t *testing.T) {
	f := newFamily()
	m := newMedia(false)
	m.searching = true
	c := serve(t, setup{media: m, family: f})

	id := cast(t, c, pagesOf("https://site.example/watch"))
	if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: id}); err != nil {
		t.Fatal(err)
	}
	if w := watch(t, c, id, nil); w.ended.GetOutcome() != castorv1.Outcome_OUTCOME_STOPPED {
		t.Errorf("the cast ended %v, want stopped", w.ended)
	}
	select {
	case got := <-f.played:
		t.Errorf("the device played %q for a cast stopped before it found anything", got)
	default:
	}
}

func TestAServerShuttingDownFailsItsCastsReleasingTheirDevicesAndTakesNoMore(t *testing.T) {
	f := newFamily()
	c := serve(t, setup{media: newMedia(true), family: f})

	id := cast(t, c, streamOf("https://cdn.example/direct"))
	<-f.played
	c.server.Shutdown(t.Context())
	select {
	case <-f.closed:
	default:
		t.Error("the server shut down before letting go of the device it cast on")
	}
	if w := watch(t, c, id, nil); w.ended.GetOutcome() != castorv1.Outcome_OUTCOME_FAILED || w.ended.GetReason() != "server shutting down" {
		t.Errorf("the cast ended %v, want it failed as the server shut down", w.ended)
	}
	if _, err := c.casts.Cast(t.Context(), &castorv1.CastRequest{Target: onBedroom, Source: streamOf("https://cdn.example/direct")}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("a cast asked of a server shutting down was met with %v, want unavailable", err)
	}
}

func TestACastOnADeviceThatCannotBeReachedFailsSayingSoAndStartsNothing(t *testing.T) {
	f := newFamily()
	f.unreachable = true
	m := newMedia(false)
	c := serve(t, setup{media: m, family: f})

	w := watch(t, c, cast(t, c, streamOf("https://cdn.example/direct")), nil)
	if w.ended.GetOutcome() != castorv1.Outcome_OUTCOME_FAILED || !strings.Contains(w.ended.GetReason(), "no answer from the device") {
		t.Errorf("the cast ended %v, want it failed for the device that never answered", w.ended)
	}
	select {
	case <-m.asked:
		t.Error("a cast whose device could not be reached started one on the media server")
	default:
	}
}

func TestADeviceWhoseConnectionIsGoneIsConnectedAgainToPlay(t *testing.T) {
	f := newFamily()
	f.goneFirst = true
	c := serve(t, setup{media: newMedia(false), family: f})

	w := watch(t, c, cast(t, c, streamOf("https://cdn.example/direct")), nil)
	if w.ended.GetOutcome() != castorv1.Outcome_OUTCOME_ENDED {
		t.Errorf("the cast ended %v, want it played on the device connected again", w.ended)
	}
	select {
	case got := <-f.played:
		if got != "https://cdn.example/direct" || f.connections() != 2 {
			t.Errorf("the device played %q over %d connections, want the stream over a second one", got, f.connections())
		}
	default:
		t.Fatal("the device never played: its gone connection was not replaced")
	}
	if gone := <-f.closed; gone != 1 {
		t.Errorf("connection %d was closed first, want the one that was gone", gone)
	}
}

// refusing answers every media server request as a media server with another token would.
func refusing(http.Handler) http.Handler {
	refusal := connect.NewErrorWriter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = refusal.Write(w, r, connect.NewError(connect.CodeUnauthenticated, errors.New("this server asks for its bearer token")))
	})
}

func TestAMediaServerRefusingThisServerIsThisServersFaultWithAHint(t *testing.T) {
	c := serve(t, setup{media: newMedia(false), family: newFamily(), guard: refusing})

	_, err := c.casts.Resolve(t.Context(), &castorv1.ResolveRequest{Source: streamOf("https://cdn.example/direct")})
	if connect.CodeOf(err) != connect.CodeInternal || !strings.Contains(err.Error(), "server.token") {
		t.Errorf("a resolve the media server refused was met with %v, want an internal error naming server.token", err)
	}
	if w := watch(t, c, cast(t, c, streamOf("https://cdn.example/direct")), nil); !strings.Contains(w.ended.GetReason(), "server.token") {
		t.Errorf("a cast the media server refused ended %v, want its reason naming server.token", w.ended)
	}
}

// losingFirstStop loses the first stop sent to the media server, as a dropped connection would.
func losingFirstStop(next http.Handler) http.Handler {
	var lost atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == mediav1connect.CastServiceStopProcedure && lost.CompareAndSwap(false, true) {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

func TestAStopLostOnTheWayToTheMediaServerIsSentAgain(t *testing.T) {
	f := newFamily()
	m := newMedia(true)
	c := serve(t, setup{media: m, family: f, guard: losingFirstStop})

	id := cast(t, c, streamOf("https://cdn.example/direct"))
	<-f.played
	if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: id}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("the media server's cast played on after its stop was lost")
	}
	if w := watch(t, c, id, nil); w.ended.GetOutcome() != castorv1.Outcome_OUTCOME_STOPPED {
		t.Errorf("the cast ended %v, want stopped", w.ended)
	}
}
