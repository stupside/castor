package apiserver_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/gen/castor/media/v1/mediav1connect"
	scrapingv1 "github.com/stupside/castor/gen/castor/scraping/v1"
	"github.com/stupside/castor/gen/castor/scraping/v1/scrapingv1connect"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/gen/castor/v1/castorv1connect"
	"github.com/stupside/castor/services/apiserver"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/mediaclient"
	"github.com/stupside/castor/services/apiserver/internal/scrapingclient"
)

// family is the one device on the network: each connection to it plays what it is handed until its cast lets it go.
type family struct {
	closeErr error
	played   chan string
	closed   chan int
	devices  []device.Info
	// stops counts the stops the device was sent.
	stops atomic.Int32
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

func (f *family) Discover(context.Context) []device.Info {
	if len(f.devices) > 0 {
		return f.devices
	}
	return []device.Info{bedroom}
}

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

func (c *connection) Play(_ context.Context, playback device.Playback) error {
	if c.gone {
		return &device.Gone{Device: "Bedroom", Observed: "the connection closed"}
	}
	c.f.played <- playback.URL.String()
	return nil
}

func (*connection) Position(context.Context) (device.Playhead, error) {
	return device.Playhead{}, device.ErrNoPosition
}

func (*connection) AwaitEnd(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (*connection) Capabilities() *mediav1.Capabilities {
	return &mediav1.Capabilities{Formats: []*mediav1.FormatSupport{{Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MP4}}, Tracks: &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: device.H264Support(), Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_AAC}}}}}}
}

func (c *connection) Close() error {
	c.f.closed <- c.n
	return c.f.closeErr
}

// media is the media server at its contract: a cast plays its best stream on the lent device, then ends unless held, until it is stopped.
type media struct {
	held bool
	// watchUpdate overrides the normal watch for malformed-response tests.
	watchUpdate  *castorv1.WatchResponse
	driveCommand *mediav1.DeviceCommand
	// asked is every cast's and ranking's resolved settings, as the media server was asked them.
	asked   chan *mediav1.PlaybackSettings
	sources chan *mediav1.Source
	// stopped closes once a stop reaches a cast.
	stopped chan struct{}
	stop    func()

	mu    sync.Mutex
	casts map[string]*mediaCast
}

func newMedia(held bool) *media {
	stopped := make(chan struct{})
	return &media{held: held, asked: make(chan *mediav1.PlaybackSettings, 8), sources: make(chan *mediav1.Source, 8), stopped: stopped, stop: sync.OnceFunc(func() { close(stopped) }), casts: map[string]*mediaCast{}}
}

// ranked reverses candidates, so a handoff proves the media server chose the order.
func ranked(source *mediav1.Source) []*castorv1.RankedStream {
	if stream := source.GetStream(); stream != nil {
		return []*castorv1.RankedStream{{Url: stream.GetUrl()}}
	}
	var out []*castorv1.RankedStream
	for _, stream := range source.GetStreams().GetStreams() {
		out = append(out, &castorv1.RankedStream{Url: stream.GetStream().GetUrl()})
	}
	slices.Reverse(out)
	return out
}

func (m *media) Rank(_ context.Context, req *mediav1.RankRequest) (*mediav1.RankResponse, error) {
	m.asked <- req.GetSettings()
	m.sources <- proto.CloneOf(req.GetSource())
	return &mediav1.RankResponse{Ranked: ranked(req.GetSource())}, nil
}

func (m *media) Start(_ context.Context, req *mediav1.StartRequest) (*mediav1.StartResponse, error) {
	m.asked <- req.GetSettings()
	m.sources <- proto.CloneOf(req.GetSource())
	c := &mediaCast{source: req.GetSource(), answers: make(chan *mediav1.AnswerRequest, 1), more: make(chan struct{}), done: make(chan struct{})}
	c.status(&castorv1.CastStatus{State: &castorv1.CastStatus_Measuring{Measuring: &castorv1.MeasuringStatus{}}})
	m.mu.Lock()
	defer m.mu.Unlock()
	m.casts[req.GetCastId()] = c
	return &mediav1.StartResponse{}, nil
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

// Stop ends the cast stopped.
func (m *media) Stop(_ context.Context, req *mediav1.StopRequest) (*mediav1.StopResponse, error) {
	c, err := m.find(req.GetCastId())
	if err != nil {
		return nil, err
	}
	m.stop()
	c.end(&castorv1.Ended{Result: &castorv1.Ended_Stopped{Stopped: &emptypb.Empty{}}})
	return &mediav1.StopResponse{}, nil
}

func (m *media) Watch(ctx context.Context, req *castorv1.WatchRequest, out *connect.ServerStream[castorv1.WatchResponse]) error {
	c, err := m.find(req.GetCastId())
	if err != nil {
		return err
	}
	if m.watchUpdate != nil {
		return out.Send(m.watchUpdate)
	}
	for next := 0; ; {
		updates, more := c.since(next)
		next += len(updates)
		for _, u := range updates {
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

// Drive plays the cast's best stream on the lent device and leads it until the cast ends.
func (m *media) Drive(ctx context.Context, req *mediav1.DriveRequest, out *connect.ServerStream[mediav1.DriveResponse]) error {
	c, err := m.find(req.GetCastId())
	if err != nil {
		return err
	}
	play := &mediav1.DeviceCommand_Play{Url: ranked(c.source)[0].GetUrl(), Stream: &mediav1.DeviceCommand_Play_Source_{Source: &mediav1.DeviceCommand_Play_Source{Container: mediav1.Container_CONTAINER_HLS}}}
	cmd := m.driveCommand
	if cmd == nil {
		cmd = &mediav1.DeviceCommand{Command: &mediav1.DeviceCommand_Call_{Call: &mediav1.DeviceCommand_Call{Id: "play", Operation: &mediav1.DeviceCommand_Call_Play{Play: play}}}}
	}
	if err := out.Send(&mediav1.DriveResponse{Command: cmd}); err != nil {
		return err
	}
	select {
	case a := <-c.answers:
		if e := a.GetError(); e != nil {
			c.end(&castorv1.Ended{Result: &castorv1.Ended_Failed{Failed: &castorv1.Failure{Code: castorv1.FailureCode_FAILURE_CODE_PLAYBACK_FAILED, Message: e.GetMessage()}}})
			break
		}
		c.status(&castorv1.CastStatus{State: &castorv1.CastStatus_Casting{Casting: &castorv1.CastingStatus{Attempt: 1}}})
		if !m.held {
			c.end(&castorv1.Ended{Result: &castorv1.Ended_Completed{Completed: &emptypb.Empty{}}})
		}
	case <-c.done:
	case <-ctx.Done():
		return ctx.Err()
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
	source  *mediav1.Source
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
	media    *media
	family   *family
	guard    func(http.Handler) http.Handler
	resolver resolveFunc
	// cast is the cast section the API server runs on; unset, the one castor ships.
	cast apiserver.CastConfig
	// uncheckedMedia simulates a downstream server that sends malformed responses.
	uncheckedMedia bool
}

var shipped = apiserver.CastConfig{Delivery: apiserver.Delivery(castorv1.Delivery_DELIVERY_AUTO), MaxHeight: 1080}

func preferred(languages ...string) *castorv1.SubtitleSelection {
	return &castorv1.SubtitleSelection{Mode: &castorv1.SubtitleSelection_Preferences{Preferences: &castorv1.SubtitlePreferences{Languages: languages}}}
}

func preferredWithFallback(languages ...string) *castorv1.SubtitleSelection {
	selection := preferred(languages...)
	selection.GetPreferences().TranscribeIfMissing = true
	return selection
}

// serve runs the media server's contract and the API server before it, as castor does, and returns a client of the API.
func serve(t *testing.T, s setup) api {
	t.Helper()
	// Both ways, every message is held to the rules the contract states, as the media server holds them.
	valid := connect.WithInterceptors(validate.NewInterceptor(validate.WithValidateResponses()))
	mediaOptions := []connect.HandlerOption{valid}
	if s.uncheckedMedia {
		mediaOptions = nil
	}
	mux := http.NewServeMux()
	mux.Handle(mediav1connect.NewCastServiceHandler(s.media, mediaOptions...))
	mux.Handle(mediav1connect.NewDeviceServiceHandler(s.media, mediaOptions...))
	mux.Handle(mediav1connect.NewStreamServiceHandler(s.media, mediaOptions...))
	var handler http.Handler = mux
	if s.guard != nil {
		handler = s.guard(handler)
	}
	mediaAPI := httptest.NewTestServer(t, handler)
	if s.resolver == nil {
		s.resolver = resolveFunc(func(_ context.Context, pages []string) ([]*castorv1.StreamCandidate, error) {
			return []*castorv1.StreamCandidate{
				{Stream: &castorv1.Stream{Url: "https://cdn.example/a.m3u8", Headers: map[string]string{"Cookie": "viewer=secret", "Referer": pages[0]}, ContentType: "application/x-mpegURL"}, SourcePage: pages[0], Ladder: castorv1.Ladder_LADDER_MULTIVARIANT},
				{Stream: &castorv1.Stream{Url: "https://cdn.example/b.m3u8"}, SourcePage: pages[0]},
			}, nil
		})
	}
	scrapingMux := http.NewServeMux()
	scrapingMux.Handle(scrapingv1connect.NewScrapingServiceHandler(s.resolver, valid))
	scrapingAPI := httptest.NewTestServer(t, scrapingMux)
	cast := s.cast
	if reflect.DeepEqual(cast, apiserver.CastConfig{}) {
		cast = shipped
	}
	families := []device.Family{s.family}
	srv, err := apiserver.New(apiserver.Backend{
		Devices:  device.Registry{Families: families, Timeout: time.Second},
		Discover: device.NetworkDiscovery(families),
		Media:    mediaclient.New(mediaAPI.Client(), mediaAPI.URL),
		Scraping: scrapingclient.New(scrapingAPI.Client(), scrapingAPI.URL),
	}, cast)
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

type resolveFunc func(context.Context, []string) ([]*castorv1.StreamCandidate, error)

func (f resolveFunc) Resolve(ctx context.Context, req *scrapingv1.ResolveRequest) (*scrapingv1.ResolveResponse, error) {
	streams, err := f(ctx, req.GetUrls())
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &scrapingv1.ResolveResponse{Streams: streams}, nil
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
	ended *castorv1.Ended
}

// watch follows cast id to its end.
func watch(t *testing.T, c api, id string) watched {
	t.Helper()
	stream, err := c.casts.Watch(t.Context(), &castorv1.WatchRequest{CastId: id})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var w watched
	for stream.Receive() {
		switch u := stream.Msg().GetUpdate().(type) {
		case *castorv1.WatchResponse_Status:
			w.shown = append(w.shown, u.Status)
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
		if stateOrder(t, shown[i]) < stateOrder(t, shown[i-1]) {
			t.Errorf("the cast went back from %v to %v", shown[i-1], shown[i])
		}
	}
}

func stateOrder(t *testing.T, status *castorv1.CastStatus) int {
	t.Helper()
	switch status.GetState().(type) {
	case *castorv1.CastStatus_Connecting:
		return 0
	case *castorv1.CastStatus_Extracting:
		return 1
	case *castorv1.CastStatus_Measuring:
		return 2
	case *castorv1.CastStatus_Casting:
		return 3
	default:
		t.Fatalf("status has no state: %v", status)
		return -1
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
	if len(devices) != 1 || devices[0].GetId() != "dlna:uuid-1" || devices[0].GetName() != "Bedroom" || devices[0].GetType() != castorv1.DeviceType_DEVICE_TYPE_DLNA {
		t.Fatalf("listed %v, want the bedroom device under its family and identity", devices)
	}
	w := watch(t, c, cast(t, c, streamOf("https://cdn.example/direct")))
	if w.ended.GetCompleted() == nil {
		t.Errorf("the cast ended %v, want ended", w.ended)
	}
	forward(t, w.shown)
	for _, s := range w.shown {
		if s.GetExtracting() != nil {
			t.Errorf("a cast of a stream showed %v", s)
		}
	}
	if last := w.shown[len(w.shown)-1]; last.GetCasting() == nil || last.GetCasting().GetAttempt() != 1 {
		t.Errorf("the watcher was last shown %v, want the first attempt casting", last)
	}
	if got := <-f.played; got != "https://cdn.example/direct" {
		t.Errorf("the device played %q, want the stream as is", got)
	}
}

func TestPagesAreSearchedThenTheirBestStreamIsCast(t *testing.T) {
	f := newFamily()
	m := newMedia(false)
	c := serve(t, setup{media: m, family: f})

	resolved, err := c.casts.Resolve(t.Context(), &castorv1.ResolveRequest{Source: pagesOf("https://site.example/watch")})
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.GetRanked(); len(got) != 2 || got[0].GetUrl() != "https://cdn.example/b.m3u8" {
		t.Errorf("resolved %v, want the page's streams in the media server's order", got)
	}
	for _, ranked := range resolved.GetRanked() {
		if ranked.Bitrate != nil {
			t.Errorf("a ranking without a measurement reported a bitrate: %v", ranked)
		}
	}

	started, err := c.casts.Cast(t.Context(), &castorv1.CastRequest{
		Target: &castorv1.Target{Target: &castorv1.Target_Pinned_{Pinned: &castorv1.Target_Pinned{Type: castorv1.DeviceType_DEVICE_TYPE_DLNA, Address: "10.0.0.9"}}},
		Source: pagesOf("https://site.example/watch"),
	})
	if err != nil {
		t.Fatal(err)
	}
	w := watch(t, c, started.GetCastId())
	if w.ended.GetCompleted() == nil {
		t.Errorf("the cast ended %v, want ended", w.ended)
	}
	forward(t, w.shown)
	if got := <-f.played; got != "https://cdn.example/b.m3u8" {
		t.Errorf("the device played %q, want the ranking's head", got)
	}
	for range 2 {
		handed := (<-m.sources).GetStreams().GetStreams()
		if len(handed) != 2 {
			t.Fatalf("media did not receive candidates: %v", handed)
		}
		first := handed[0]
		if first.GetSourcePage() != "https://site.example/watch" || first.GetStream().GetHeaders()["Cookie"] != "viewer=secret" || first.GetStream().GetHeaders()["Referer"] != first.GetSourcePage() || first.GetStream().GetContentType() != "application/x-mpegURL" || first.GetLadder() != castorv1.Ladder_LADDER_MULTIVARIANT {
			t.Errorf("API lost capture metadata before media: %v", first)
		}
	}
}

func TestDirectAndResolvedSourcesBypassExtraction(t *testing.T) {
	var extractions atomic.Int32
	m := newMedia(false)
	c := serve(t, setup{media: m, family: newFamily(), resolver: resolveFunc(func(context.Context, []string) ([]*castorv1.StreamCandidate, error) {
		extractions.Add(1)
		return nil, errors.New("should not extract")
	})})
	ready := &castorv1.Source{Source: &castorv1.Source_Streams{Streams: &castorv1.Source_ResolvedStreams{Streams: []*castorv1.StreamCandidate{{Stream: &castorv1.Stream{Url: "https://cdn.example/ready.m3u8", Headers: map[string]string{"Cookie": "secret"}}}}}}}
	for _, source := range []*castorv1.Source{streamOf("https://cdn.example/direct.mp4"), ready} {
		if _, err := c.casts.Resolve(t.Context(), &castorv1.ResolveRequest{Source: source}); err != nil {
			t.Fatal(err)
		}
		if w := watch(t, c, cast(t, c, source)); w.ended.GetCompleted() == nil {
			t.Fatal(w.ended)
		}
	}
	if extractions.Load() != 0 {
		t.Fatal("a resolved source opened the browser")
	}
}

func TestFailedExtractionNeverReachesMedia(t *testing.T) {
	m := newMedia(false)
	f := newFamily()
	c := serve(t, setup{media: m, family: f, resolver: resolveFunc(func(context.Context, []string) ([]*castorv1.StreamCandidate, error) {
		return nil, errors.New("no playable stream")
	})})
	pages := pagesOf("https://site.example/empty")
	if _, err := c.casts.Resolve(t.Context(), &castorv1.ResolveRequest{Source: pages}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("resolve: %v, want not found", err)
	}
	w := watch(t, c, cast(t, c, pages))
	if w.ended.GetFailed().GetCode() != castorv1.FailureCode_FAILURE_CODE_EXTRACTION_FAILED || !strings.Contains(w.ended.GetFailed().GetMessage(), "no playable stream") {
		t.Fatalf("extraction failed without an explanation: %v", w.ended)
	}
	select {
	case source := <-m.sources:
		t.Fatalf("media received failed extraction: %v", source)
	default:
	}
	select {
	case played := <-f.played:
		t.Fatalf("device played after extraction failed: %s", played)
	default:
	}
}

func TestListingResolvedCandidatesRedactsCredentialsWithoutChangingPlayback(t *testing.T) {
	m := newMedia(true)
	f := newFamily()
	c := serve(t, setup{media: m, family: f})
	source := &castorv1.Source{Source: &castorv1.Source_Streams{Streams: &castorv1.Source_ResolvedStreams{Streams: []*castorv1.StreamCandidate{{Stream: &castorv1.Stream{Url: "https://cdn.example/master.m3u8", Headers: map[string]string{"Cookie": "secret", "Authorization": "Bearer private"}}}}}}}
	id := cast(t, c, source)
	<-f.played
	for range 2 {
		listed, err := c.casts.ListCasts(t.Context(), &castorv1.ListCastsRequest{})
		if err != nil || len(listed.GetCasts()) != 1 {
			t.Fatalf("listing: %v %v", listed, err)
		}
		if headers := listed.Casts[0].Source.GetStreams().Streams[0].GetStream().GetHeaders(); len(headers) != 0 {
			t.Fatalf("listing leaked candidate credentials: %v", headers)
		}
	}
	if headers := (<-m.sources).GetStreams().Streams[0].GetStream().GetHeaders(); headers["Cookie"] != "secret" || headers["Authorization"] != "Bearer private" {
		t.Fatalf("listing stripped playback credentials: %v", headers)
	}
	if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: id}); err != nil {
		t.Fatal(err)
	}
	watch(t, c, id)
}

func TestRequestPreferencesOverrideTheDefaultsFieldByField(t *testing.T) {
	m := newMedia(false)
	c := serve(t, setup{media: m, family: newFamily()})

	for _, tc := range []struct {
		asked *castorv1.Preferences
		want  *mediav1.PlaybackSettings
	}{
		{nil, &mediav1.PlaybackSettings{Delivery: castorv1.Delivery_DELIVERY_AUTO, MaxHeight: 1080, Subtitles: &castorv1.SubtitleSelection{Mode: &castorv1.SubtitleSelection_Disabled{Disabled: &emptypb.Empty{}}}}},
		{&castorv1.Preferences{MaxHeight: new(uint32(720))}, &mediav1.PlaybackSettings{Delivery: castorv1.Delivery_DELIVERY_AUTO, MaxHeight: 720, Subtitles: &castorv1.SubtitleSelection{Mode: &castorv1.SubtitleSelection_Disabled{Disabled: &emptypb.Empty{}}}}},
		{
			&castorv1.Preferences{Delivery: castorv1.Delivery_DELIVERY_SERVE.Enum(), Subtitles: preferred("fr")},
			&mediav1.PlaybackSettings{Delivery: castorv1.Delivery_DELIVERY_SERVE, MaxHeight: 1080, Subtitles: preferred("fr")},
		},
		{
			&castorv1.Preferences{Subtitles: preferredWithFallback("en")},
			&mediav1.PlaybackSettings{Delivery: castorv1.Delivery_DELIVERY_AUTO, MaxHeight: 1080, Subtitles: preferredWithFallback("en")},
		},
	} {
		if _, err := c.casts.Resolve(t.Context(), &castorv1.ResolveRequest{Source: streamOf("https://cdn.example/direct"), Preferences: tc.asked}); err != nil {
			t.Fatal(err)
		}
		if got := <-m.asked; !proto.Equal(got, tc.want) {
			t.Errorf("asking %v resolved as %v, want %v", tc.asked, got, tc.want)
		}
		<-m.sources
		started, err := c.casts.Cast(t.Context(), &castorv1.CastRequest{Target: onBedroom, Source: streamOf("https://cdn.example/direct"), Preferences: tc.asked})
		if err != nil {
			t.Fatal(err)
		}
		if w := watch(t, c, started.GetCastId()); w.ended.GetCompleted() == nil {
			t.Fatal(w.ended)
		}
		if got := <-m.asked; !proto.Equal(got, tc.want) {
			t.Errorf("asking %v cast as %v, want %v", tc.asked, got, tc.want)
		}
		<-m.sources
	}
}

func TestPreferredAudioAndSubtitleLanguagesReachMedia(t *testing.T) {
	m := newMedia(false)
	c := serve(t, setup{media: m, family: newFamily()})
	preferences := &castorv1.Preferences{
		Audio:     &castorv1.AudioSelection{Languages: []string{"fr", "en"}},
		Subtitles: preferred("fr", "en"),
	}
	if _, err := c.casts.Resolve(t.Context(), &castorv1.ResolveRequest{Source: streamOf("https://cdn.example/direct"), Preferences: preferences}); err != nil {
		t.Fatal(err)
	}
	got := <-m.asked
	if !proto.Equal(got.GetAudio(), preferences.Audio) || !proto.Equal(got.GetSubtitles(), preferences.Subtitles) {
		t.Errorf("media preferences: audio=%v subtitles=%v", got.GetAudio(), got.GetSubtitles())
	}
}

func TestSubtitleModesOverrideOrInheritTheConfiguredLanguage(t *testing.T) {
	for name, tc := range map[string]struct {
		preferences *castorv1.Preferences
		want        *castorv1.SubtitleSelection
	}{
		"omitted preferences": {nil, preferred("fr")},
		"omitted subtitles":   {&castorv1.Preferences{MaxHeight: proto.Uint32(720)}, preferred("fr")},
		"disabled":            {&castorv1.Preferences{Subtitles: &castorv1.SubtitleSelection{Mode: &castorv1.SubtitleSelection_Disabled{Disabled: &emptypb.Empty{}}}}, &castorv1.SubtitleSelection{Mode: &castorv1.SubtitleSelection_Disabled{Disabled: &emptypb.Empty{}}}},
		"language":            {&castorv1.Preferences{Subtitles: preferred("en")}, preferred("en")},
	} {
		t.Run(name, func(t *testing.T) {
			m := newMedia(false)
			c := serve(t, setup{media: m, family: newFamily(), cast: apiserver.CastConfig{Delivery: apiserver.Delivery(castorv1.Delivery_DELIVERY_SERVE), MaxHeight: 720, Subtitles: apiserver.SubtitleConfig{Languages: []string{"fr"}}}})
			want := &mediav1.PlaybackSettings{Delivery: castorv1.Delivery_DELIVERY_SERVE, MaxHeight: 720, Subtitles: tc.want}
			source := streamOf("https://cdn.example/direct")
			if _, err := c.casts.Resolve(t.Context(), &castorv1.ResolveRequest{Source: source, Preferences: tc.preferences}); err != nil {
				t.Fatal(err)
			}
			if got := <-m.asked; !proto.Equal(got, want) {
				t.Errorf("Resolve asked media for %v, want %v", got, want)
			}
			started, err := c.casts.Cast(t.Context(), &castorv1.CastRequest{Target: onBedroom, Source: source, Preferences: tc.preferences})
			if err != nil {
				t.Fatal(err)
			}
			if w := watch(t, c, started.GetCastId()); w.ended.GetCompleted() == nil {
				t.Fatal(w.ended)
			}
			if got := <-m.asked; !proto.Equal(got, want) {
				t.Errorf("Cast asked media for %v, want %v", got, want)
			}
		})
	}
}

func TestSubtitleFallbackCanBeOverriddenPerCast(t *testing.T) {
	m := newMedia(false)
	c := serve(t, setup{media: m, family: newFamily(), cast: apiserver.CastConfig{
		Delivery: apiserver.Delivery(castorv1.Delivery_DELIVERY_AUTO), MaxHeight: 1080,
		Subtitles: apiserver.SubtitleConfig{Languages: []string{"en"}, TranscribeIfMissing: true},
	}})
	for _, tc := range []struct {
		selection *castorv1.SubtitleSelection
		want      bool
	}{
		{nil, true},
		{preferred("en"), false},
		{preferredWithFallback("en"), true},
	} {
		_, err := c.casts.Resolve(t.Context(), &castorv1.ResolveRequest{
			Source:      streamOf("https://cdn.example/direct"),
			Preferences: &castorv1.Preferences{Subtitles: tc.selection},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := (<-m.asked).GetSubtitles().GetPreferences().GetTranscribeIfMissing(); got != tc.want {
			t.Errorf("transcription = %v, want %v", got, tc.want)
		}
	}
}

func TestBrokenMediaWatchStopsTheCast(t *testing.T) {
	m := newMedia(true)
	m.watchUpdate = &castorv1.WatchResponse{Update: &castorv1.WatchResponse_Status{Status: &castorv1.CastStatus{}}}
	f := newFamily()
	c := serve(t, setup{media: m, family: f, uncheckedMedia: true})
	w := watch(t, c, cast(t, c, streamOf("https://cdn.example/direct")))
	if w.ended.GetFailed().GetCode() != castorv1.FailureCode_FAILURE_CODE_INTERNAL || w.ended.GetFailed().GetMessage() == "" {
		t.Fatalf("broken media watch ended the public cast as %v, want an internal failure", w.ended)
	}
	select {
	case <-m.stopped:
	default:
		t.Error("a broken media watch left its cast running")
	}
	select {
	case <-f.closed:
	default:
		t.Error("a broken media watch left its device connected")
	}
	if f.stops.Load() != 1 {
		t.Errorf("the device was sent %d stops as its cast ended, want 1", f.stops.Load())
	}
}

func TestMalformedDeviceCommandsFailTheCastBeforeTouchingTheDevice(t *testing.T) {
	m, f := newMedia(true), newFamily()
	m.driveCommand = &mediav1.DeviceCommand{Command: &mediav1.DeviceCommand_Call_{Call: &mediav1.DeviceCommand_Call{Id: "play", Operation: &mediav1.DeviceCommand_Call_Play{Play: &mediav1.DeviceCommand_Play{Url: "/stream"}}}}}
	c := serve(t, setup{media: m, family: f, uncheckedMedia: true})
	w := watch(t, c, cast(t, c, streamOf("https://cdn.example/direct")))
	if w.ended.GetFailed().GetCode() != castorv1.FailureCode_FAILURE_CODE_INTERNAL {
		t.Fatalf("malformed command ended the cast as %v", w.ended)
	}
	select {
	case played := <-f.played:
		t.Fatalf("malformed downstream command played %q on the device", played)
	default:
	}
	select {
	case <-m.stopped:
	default:
		t.Error("a malformed command left the media cast running")
	}
	select {
	case <-f.closed:
	default:
		t.Error("a malformed command left its device connected")
	}
}

func TestALateWatcherLearnsWhereTheCastStoodAndHowItEnded(t *testing.T) {
	c := serve(t, setup{media: newMedia(false), family: newFamily()})

	id := cast(t, c, streamOf("https://cdn.example/direct"))
	first := watch(t, c, id)
	late := watch(t, c, id)
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
	synctest.Test(t, func(t *testing.T) {
		f := newFamily()
		c := serve(t, setup{media: newMedia(true), family: f})

		id := cast(t, c, streamOf("https://cdn.example/direct"))
		<-f.played
		if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: id}); err != nil {
			t.Fatal(err)
		}
		if w := watch(t, c, id); w.ended.GetStopped() == nil {
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
	})
}

func TestCastsAreListedOldestFirstWithoutTheHeadersTheirStreamsWereFoundWith(t *testing.T) {
	f := newFamily()
	f.devices = []device.Info{bedroom, {ID: "uuid-2", Name: "Living room", Type: "dlna", Address: "10.0.0.10"}}
	c := serve(t, setup{media: newMedia(true), family: f})

	var ids []string
	for i, raw := range []string{"https://cdn.example/first", "https://cdn.example/second"} {
		source := streamOf(raw)
		source.GetStream().Headers = map[string]string{"Cookie": "session=viewer"}
		started, err := c.casts.Cast(t.Context(), &castorv1.CastRequest{
			Target: &castorv1.Target{Target: &castorv1.Target_DeviceId{DeviceId: "dlna:" + f.devices[i].ID}},
			Source: source,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, started.GetCastId())
		select {
		case <-f.played:
		case <-time.After(5 * time.Second):
			t.Fatal("cast did not start on its receiver")
		}
	}
	listed, err := c.casts.ListCasts(t.Context(), &castorv1.ListCastsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	got := listed.GetCasts()
	if len(got) != 2 || got[0].GetId() != ids[0] || got[1].GetId() != ids[1] || got[0].GetDevice().GetName() != "Bedroom" || got[1].GetDevice().GetName() != "Living room" {
		t.Fatalf("listed %v, want both receivers' casts, oldest first", got)
	}
	for _, listed := range got {
		if h := listed.GetSource().GetStream().GetHeaders(); len(h) != 0 {
			t.Errorf("a listed cast showed the headers %v its stream was found with", h)
		}
	}
}

func TestACastStoppedWhileItsPagesAreSearchedEndsStoppedWithoutPlaying(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFamily()
		m := newMedia(false)
		looking, canceled := make(chan struct{}), make(chan struct{})
		c := serve(t, setup{media: m, family: f, resolver: resolveFunc(func(ctx context.Context, _ []string) ([]*castorv1.StreamCandidate, error) {
			close(looking)
			<-ctx.Done()
			close(canceled)
			return nil, ctx.Err()
		})})

		id := cast(t, c, pagesOf("https://site.example/watch"))
		select {
		case <-looking:
		case <-time.After(5 * time.Second):
			t.Fatal("extraction did not start")
		}
		listed, err := c.casts.ListCasts(t.Context(), &castorv1.ListCastsRequest{})
		if err != nil || len(listed.GetCasts()) != 1 || listed.Casts[0].GetStatus().GetExtracting() == nil {
			t.Fatalf("while resolving: %v %v, want extracting", listed, err)
		}
		if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: id}); err != nil {
			t.Fatal(err)
		}
		if w := watch(t, c, id); w.ended.GetStopped() == nil {
			t.Errorf("the cast ended %v, want stopped", w.ended)
		}
		select {
		case <-canceled:
		case <-time.After(5 * time.Second):
			t.Fatal("stopping did not cancel scrapingserver's extraction")
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if len(m.casts) != 0 {
			t.Error("media received a cast before extraction completed")
		}
		select {
		case got := <-f.played:
			t.Errorf("the device played %q for a cast stopped before it found anything", got)
		default:
		}
	})
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
	if w := watch(t, c, id); w.ended.GetFailed().GetCode() != castorv1.FailureCode_FAILURE_CODE_SERVER_SHUTDOWN || w.ended.GetFailed().GetMessage() != "server shutting down" {
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

	w := watch(t, c, cast(t, c, streamOf("https://cdn.example/direct")))
	if w.ended.GetFailed().GetCode() != castorv1.FailureCode_FAILURE_CODE_DEVICE_UNREACHABLE || !strings.Contains(w.ended.GetFailed().GetMessage(), "no answer from the device") {
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

	w := watch(t, c, cast(t, c, streamOf("https://cdn.example/direct")))
	if w.ended.GetCompleted() == nil {
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
	if connect.CodeOf(err) != connect.CodeInternal || !strings.Contains(err.Error(), "servers.media.token") {
		t.Errorf("a resolve the media server refused was met with %v, want an internal error naming servers.media.token", err)
	}
	if w := watch(t, c, cast(t, c, streamOf("https://cdn.example/direct"))); w.ended.GetFailed().GetCode() != castorv1.FailureCode_FAILURE_CODE_INTERNAL || !strings.Contains(w.ended.GetFailed().GetMessage(), "servers.media.token") {
		t.Errorf("a cast the media server refused ended %v, want its reason naming servers.media.token", w.ended)
	}
}

func TestALostMediaAnswerDoesNotBlameADeviceThatPlayed(t *testing.T) {
	f := newFamily()
	guard := func(next http.Handler) http.Handler {
		refusal := connect.NewErrorWriter()
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == mediav1connect.DeviceServiceAnswerProcedure {
				_ = refusal.Write(w, r, connect.NewError(connect.CodeUnavailable, errors.New("media answer unavailable")))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	c := serve(t, setup{media: newMedia(true), family: f, guard: guard})
	w := watch(t, c, cast(t, c, streamOf("https://cdn.example/direct")))
	if failure := w.ended.GetFailed(); failure.GetCode() != castorv1.FailureCode_FAILURE_CODE_INTERNAL || !strings.Contains(failure.GetMessage(), "answering device command") {
		t.Fatalf("lost media answer ended %v, want an internal failure", w.ended)
	}
	select {
	case played := <-f.played:
		if played != "https://cdn.example/direct" {
			t.Errorf("device played %q, want the supplied stream", played)
		}
	default:
		t.Fatal("the device never played")
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
	synctest.Test(t, func(t *testing.T) {
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
		if w := watch(t, c, id); w.ended.GetStopped() == nil {
			t.Errorf("the cast ended %v, want stopped", w.ended)
		}
	})
}

func (c *connection) Stop(context.Context) error {
	c.f.stops.Add(1)
	return nil
}

func (*connection) Pause(context.Context) error  { return nil }
func (*connection) Resume(context.Context) error { return nil }

func TestCastJoinsRemoteStopBeforeReleasingDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFamily()
		m := newMedia(true)
		stopping, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		guard := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != mediav1connect.CastServiceStopProcedure {
					next.ServeHTTP(w, r)
					return
				}
				response := httptest.NewRecorder()
				next.ServeHTTP(response, r)
				close(stopping)
				<-release
				maps.Copy(w.Header(), response.Header())
				w.WriteHeader(response.Code)
				_, _ = w.Write(response.Body.Bytes())
			})
		}
		c := serve(t, setup{media: m, family: f, guard: guard})
		id := cast(t, c, streamOf("https://cdn.example/direct"))
		<-f.played
		if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: id}); err != nil {
			t.Fatal(err)
		}
		<-stopping
		synctest.Wait()
		select {
		case <-f.closed:
			t.Error("device released while remote stop callback was running")
		default:
		}
		unblock()
		if w := watch(t, c, id); w.ended.GetStopped() == nil {
			t.Fatalf("cast ended %v", w.ended)
		}
	})
}

func TestLostStartResponseStopsAcceptedMediaCast(t *testing.T) {
	f := newFamily()
	m := newMedia(true)
	guard := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != mediav1connect.CastServiceStartProcedure {
				next.ServeHTTP(w, r)
				return
			}
			response := httptest.NewRecorder()
			next.ServeHTTP(response, r)
			http.Error(w, "start response lost", http.StatusBadGateway)
		})
	}
	c := serve(t, setup{media: m, family: f, guard: guard})
	id := cast(t, c, streamOf("https://cdn.example/direct"))
	if w := watch(t, c, id); w.ended.GetFailed() == nil {
		t.Fatalf("cast ended %v", w.ended)
	}
	select {
	case <-m.stopped:
	default:
		t.Fatal("accepted media cast was abandoned after its start response was lost")
	}
}

func TestDeviceCleanupFailureReachesTheCastWatcher(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		name := "completed"
		if stopped {
			name = "stopped"
		}
		t.Run(name, func(t *testing.T) {
			f := newFamily()
			f.closeErr = errors.New("receiver final STOP failed")
			c := serve(t, setup{media: newMedia(stopped), family: f})
			id := cast(t, c, streamOf("https://cdn.example/direct"))
			<-f.played
			if stopped {
				if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: id}); err != nil {
					t.Fatal(err)
				}
			}
			result := watch(t, c, id).ended
			if failure := result.GetFailed(); failure == nil || !strings.Contains(failure.Message, f.closeErr.Error()) {
				t.Fatalf("cleanup failure hidden by %s outcome: %v", name, result)
			}
			select {
			case <-f.closed:
			default:
				t.Fatal("published outcome before device cleanup")
			}
		})
	}
}

func TestStopDuringStartReconcilesBeforeReleasingDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, m := newFamily(), newMedia(true)
		started, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		guard := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != mediav1connect.CastServiceStartProcedure {
					next.ServeHTTP(w, r)
					return
				}
				response := httptest.NewRecorder()
				next.ServeHTTP(response, r)
				close(started)
				<-release
				maps.Copy(w.Header(), response.Header())
				w.WriteHeader(response.Code)
				_, _ = w.Write(response.Body.Bytes())
			})
		}
		c := serve(t, setup{media: m, family: f, guard: guard})
		id := cast(t, c, streamOf("https://cdn.example/direct"))
		<-started
		if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: id}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		select {
		case <-f.closed:
			t.Fatal("device released while Start was unresolved")
		default:
		}
		unblock()
		if w := watch(t, c, id); w.ended.GetStopped() == nil {
			t.Fatalf("cast ended %v", w.ended)
		}
		select {
		case <-m.stopped:
		default:
			t.Fatal("accepted Start was not reconciled")
		}
		select {
		case <-f.played:
			t.Fatal("started driving after caller cancellation")
		default:
		}
	})
}

func TestLostWatchStopsRemoteCastBeforeReleasingDevice(t *testing.T) {
	f, m := newFamily(), newMedia(true)
	guard := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == mediav1connect.CastServiceWatchProcedure {
				http.Error(w, "watch connection lost", http.StatusBadGateway)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	c := serve(t, setup{media: m, family: f, guard: guard})
	id := cast(t, c, streamOf("https://cdn.example/direct"))
	if w := watch(t, c, id); w.ended.GetFailed() == nil {
		t.Fatalf("lost watch reported as remote completion: %v", w.ended)
	}
	select {
	case <-m.stopped:
	default:
		t.Fatal("remote cast abandoned after losing Watch")
	}
	select {
	case <-f.closed:
	default:
		t.Fatal("outcome published before device release")
	}
}

func TestReplacementWaitsForOldSessionAndIgnoresStaleStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, m := newFamily(), newMedia(true)
		stopping, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		var stops atomic.Int32
		guard := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == mediav1connect.CastServiceStopProcedure && stops.Add(1) == 1 {
					close(stopping)
					<-release
				}
				next.ServeHTTP(w, r)
			})
		}
		c := serve(t, setup{media: m, family: f, guard: guard})
		first := cast(t, c, streamOf("https://cdn.example/first"))
		<-f.played
		if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: first}); err != nil {
			t.Fatal(err)
		}
		<-stopping
		busy := cast(t, c, streamOf("https://cdn.example/too-early"))
		if w := watch(t, c, busy); w.ended.GetFailed().GetCode() != castorv1.FailureCode_FAILURE_CODE_PLAYBACK_FAILED {
			t.Fatalf("overlapping cast accepted: %v", w.ended)
		}
		if f.connections() != 1 {
			t.Fatal("replacement connected before old session joined")
		}
		unblock()
		if w := watch(t, c, first); w.ended.GetStopped() == nil {
			t.Fatalf("first cast ended %v", w.ended)
		}
		if closed := <-f.closed; closed != 1 {
			t.Fatalf("closed connection %d, want first", closed)
		}
		next := cast(t, c, streamOf("https://cdn.example/next"))
		if played := <-f.played; played != "https://cdn.example/next" {
			t.Fatalf("replacement played %q", played)
		}
		if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: first}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if stops.Load() != 1 || f.connections() != 2 {
			t.Fatalf("stale stop affected replacement: stops=%d connections=%d", stops.Load(), f.connections())
		}
		select {
		case <-f.closed:
			t.Fatal("stale cleanup closed replacement's device")
		default:
		}
		if _, err := c.casts.Stop(t.Context(), &castorv1.StopRequest{CastId: next}); err != nil {
			t.Fatal(err)
		}
		if w := watch(t, c, next); w.ended.GetStopped() == nil {
			t.Fatalf("replacement ended %v", w.ended)
		}
	})
}
