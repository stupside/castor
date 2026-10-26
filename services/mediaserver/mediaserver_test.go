// The media contract end to end: a real media server, its generated clients, and a fake lender with its fake device.
package mediaserver_test

import (
	"cmp"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/gen/castor/media/v1/mediav1connect"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/castid"
	"github.com/stupside/castor/services/mediaserver"
	"github.com/stupside/castor/services/mediaserver/internal/cast"
	"github.com/stupside/castor/services/mediaserver/internal/cast/execute"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

type play func(ctx context.Context, device execute.Device, listeners output.Publisher, streams []*source.Stream, turns recovery.Turns) error

// engine is the server's machinery: it ranks by reversing, so the order cast in proves ranking ran.
type engine struct {
	play     play
	asked    chan *mediav1.PlaybackSettings
	measured chan *source.Stream
	ranked   chan []*source.Stream
}

func machinery(p play) *engine {
	return &engine{play: p, asked: make(chan *mediav1.PlaybackSettings, 4), measured: make(chan *source.Stream, 1), ranked: make(chan []*source.Stream, 1)}
}

func (e *engine) backend() func(*mediav1.PlaybackSettings) cast.Caster { return e.caster }

func (e *engine) caster(asked *mediav1.PlaybackSettings) cast.Caster {
	e.asked <- asked
	return e
}

func (e *engine) Rank(ctx context.Context, streams []*source.Stream) ([]*source.Stream, error) {
	slog.InfoContext(ctx, "ranking streams", "count", len(streams))
	select {
	case e.ranked <- slices.Clone(streams):
	default:
	}
	out := slices.Clone(streams)
	slices.Reverse(out)
	out[0].LastResort = true
	return out, nil
}

func (e *engine) Measure(_ context.Context, stream *source.Stream) (*source.Stream, error) {
	e.measured <- stream
	return stream, nil
}

func (e *engine) Play(ctx context.Context, device execute.Device, l output.Publisher, streams []*source.Stream, _ time.Duration, turns recovery.Turns) error {
	// This fake skips probing: its playable fixtures are MPEG-TS, as the lent device advertises.
	for _, stream := range streams {
		stream.ContentType = media.MPEGTS
	}
	return e.play(ctx, device, l, streams, turns)
}

// lentDevice is the device the fake lender lends: it fetches what it is told to play.
type lentDevice struct {
	caps   *mediav1.Capabilities
	handed chan *url.URL
	as     chan mediav1.Container
	played chan []byte
	// end is how its playback ends, once asked; never, unless set.
	end *mediav1.DeviceError
}

func newLentDevice() *lentDevice {
	return &lentDevice{
		caps:   &mediav1.Capabilities{SelfFetch: true, ServedHeaders: map[string]string{"transferMode.dlna.org": "Streaming"}, Formats: []*mediav1.FormatSupport{{Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MPEGTS}}, Tracks: &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_H264{H264: &mediav1.H264Support{MaxLevel: 42}}}, Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_AAC, MaxChannels: 2}}}}}},
		handed: make(chan *url.URL, 4),
		as:     make(chan mediav1.Container, 4),
		played: make(chan []byte, 4),
	}
}

func (d *lentDevice) play(ctx context.Context, play *mediav1.DeviceCommand_Play) error {
	u, err := url.Parse(play.GetUrl())
	if err != nil {
		return err
	}
	d.handed <- u
	d.as <- play.GetContainer()
	if u.Scheme == "https" {
		d.played <- []byte(u.String())
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	d.played <- body
	return err
}

func (d *lentDevice) awaitEnd(ctx context.Context) *mediav1.DeviceError {
	if d.end != nil {
		return d.end
	}
	<-ctx.Done()
	return failure(ctx.Err())
}

func failure(err error) *mediav1.DeviceError {
	return &mediav1.DeviceError{Error: &mediav1.DeviceError_Message{Message: err.Error()}}
}

// server is a running media server's clients, and where devices fetch from it.
type server struct {
	casts   mediav1connect.CastServiceClient
	devices mediav1connect.DeviceServiceClient
	streams mediav1connect.StreamServiceClient
	media   string
	running *mediaserver.Server
}

func serve(t *testing.T, b func(*mediav1.PlaybackSettings) cast.Caster) server {
	t.Helper()
	lan, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	media := "http://" + lan.Addr().String()
	reach, _ := url.Parse(media)
	srv := mediaserver.New(b, reach)
	devices := httptest.NewUnstartedServer(srv.Media)
	devices.Listener.Close()
	devices.Listener = lan
	devices.Start()
	api := httptest.NewTestServer(t, srv.API)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		devices.Close()
	})
	return server{
		casts:   mediav1connect.NewCastServiceClient(api.Client(), api.URL),
		devices: mediav1connect.NewDeviceServiceClient(api.Client(), api.URL),
		streams: mediav1connect.NewStreamServiceClient(api.Client(), api.URL),
		media:   media,
		running: srv,
	}
}

// asked is what this operator asks of every cast in these tests.
var asked = &mediav1.PlaybackSettings{Delivery: castorv1.Delivery_DELIVERY_SERVE, MaxHeight: 720, Subtitles: &castorv1.SubtitleSelection{Mode: &castorv1.SubtitleSelection_Preferences{Preferences: &castorv1.SubtitlePreferences{Languages: []string{"fr"}}}}}

func streamOf(raw string) *mediav1.Source {
	return &mediav1.Source{Source: &mediav1.Source_Stream{Stream: &castorv1.Stream{Url: raw, Headers: map[string]string{"Referer": "https://page.example/"}}}}
}

func candidatesOf(urls ...string) *mediav1.Source {
	streams := make([]*castorv1.StreamCandidate, 0, len(urls))
	for _, raw := range urls {
		streams = append(streams, &castorv1.StreamCandidate{Stream: &castorv1.Stream{Url: raw, Headers: map[string]string{"Referer": "https://page.example/"}}})
	}
	return &mediav1.Source{Source: &mediav1.Source_Streams_{Streams: &mediav1.Source_Streams{Streams: streams}}}
}

func start(t *testing.T, c server, source *mediav1.Source) string {
	t.Helper()
	id := castid.New()
	if _, err := c.casts.Start(t.Context(), &mediav1.StartRequest{CastId: id, Source: source, Settings: asked}); err != nil {
		t.Fatal(err)
	}
	return id
}

// lend lends device to cast id as the API server would, running each command it is sent, until the drive ends.
func lend(ctx context.Context, c server, id string, device *lentDevice) error {
	stream, err := c.devices.Drive(ctx, &mediav1.DriveRequest{
		CastId:       id,
		Capabilities: device.caps,
	})
	if err != nil {
		return err
	}
	defer stream.Close()
	for stream.Receive() {
		cmd := stream.Msg().GetCommand()
		if cmd.GetCancel() != nil {
			continue
		}
		go func() {
			var failed *mediav1.DeviceError
			switch {
			case cmd.GetCall().GetPlay() != nil:
				if err := device.play(ctx, cmd.GetCall().GetPlay()); err != nil {
					failed = failure(err)
				}
			case cmd.GetCall().GetAwaitEnd() != nil:
				failed = device.awaitEnd(ctx)
			}
			// A lender that left answers nothing, as the API server's driver does.
			if ctx.Err() != nil {
				return
			}
			answer := &mediav1.AnswerRequest{CastId: id, CommandId: cmd.GetCall().GetId(), Answer: &mediav1.AnswerRequest_Done_{Done: &mediav1.AnswerRequest_Done{}}}
			if failed != nil {
				answer.Answer = &mediav1.AnswerRequest_Error{Error: failed}
			}
			_, _ = c.devices.Answer(context.WithoutCancel(ctx), answer)
		}()
	}
	return stream.Err()
}

// watched is what a watcher was sent of one cast, once it ended.
type watched struct {
	shown []*castorv1.CastStatus
	ended *castorv1.Ended
	err   error
}

// outcome is how the cast ended, read as an error: nil ended, why it stopped or failed otherwise.
func (w watched) outcome() error {
	switch {
	case w.err != nil:
		return w.err
	case w.ended.GetStopped() != nil:
		return errors.New("stopped")
	case w.ended.GetFailed() != nil:
		return errors.New(w.ended.GetFailed().GetMessage())
	case w.ended.GetCompleted() != nil:
		return nil
	}
	return errors.New("the cast ended without a result")
}

// watching watches cast id and returns once the first status is shown, with what it saw to come.
func watching(t *testing.T, c server, id string) <-chan watched {
	t.Helper()
	stream, err := c.casts.Watch(t.Context(), &castorv1.WatchRequest{CastId: id})
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() {
		t.Fatalf("the watch sent nothing: %v", stream.Err())
	}
	out := make(chan watched, 1)
	go func() {
		defer stream.Close()
		w := watched{shown: []*castorv1.CastStatus{stream.Msg().GetStatus()}}
		for stream.Receive() {
			switch u := stream.Msg().GetUpdate().(type) {
			case *castorv1.WatchResponse_Status:
				w.shown = append(w.shown, u.Status)
			case *castorv1.WatchResponse_Ended:
				w.ended = u.Ended
				out <- w
				return
			}
		}
		w.err = cmp.Or(stream.Err(), errors.New("the watch ended without saying how the cast did"))
		out <- w
	}()
	return out
}

// castOn starts a cast of source, lends it device and follows it to its end.
func castOn(t *testing.T, c server, source *mediav1.Source, device *lentDevice) watched {
	t.Helper()
	id := start(t, c, source)
	w := watching(t, c, id)
	lent := make(chan error, 1)
	go func() { lent <- lend(t.Context(), c, id, device) }()
	out := <-w
	if err := <-lent; err != nil {
		t.Errorf("the drive ended with %v", err)
	}
	return out
}

// handoff is a cast the device fetches the head stream of for itself.
func handoff(ctx context.Context, device execute.Device, _ output.Publisher, streams []*source.Stream, turns recovery.Turns) error {
	turns.Attempting(1)
	return device.Play(ctx, streams[0].URL, streams[0].ContentType, nil)
}

func TestPagesStreamsAreRankedThenHandedToTheLentDeviceAsTheyAre(t *testing.T) {
	device := newLentDevice()
	var caps media.Capabilities
	c := serve(t, machinery(func(ctx context.Context, lent execute.Device, l output.Publisher, streams []*source.Stream, turns recovery.Turns) error {
		caps = lent.Capabilities()
		return handoff(ctx, lent, l, streams, turns)
	}).backend())

	w := castOn(t, c, candidatesOf("https://cdn.example/a.m3u8", "https://cdn.example/b.m3u8"), device)
	if err := w.outcome(); err != nil {
		t.Fatalf("a cast that ended cleanly reported %v", err)
	}
	if last, want := w.shown[len(w.shown)-1], (&castorv1.CastStatus{State: &castorv1.CastStatus_Casting{Casting: &castorv1.CastingStatus{Streams: 2, Castable: 2, Attempt: 1}}}); !proto.Equal(last, want) {
		t.Errorf("the watcher was last shown %+v, want %+v", last, want)
	}
	if got := string(<-device.played); got != "https://cdn.example/b.m3u8" {
		t.Errorf("the device was handed %q, want the server's ranking head as is", got)
	}
	if !caps.SelfFetch || len(caps.Formats) != 1 || caps.Formats[0].Video.MaxLevel != 42 {
		t.Errorf("the engine played on %+v, want the lent device's own capabilities", caps)
	}
}

func TestALentDevicesCapabilitiesReachTheEngine(t *testing.T) {
	device := newLentDevice()
	device.caps = &mediav1.Capabilities{Deinterlaces: true, Formats: []*mediav1.FormatSupport{{Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MP4}}, Tracks: &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_H264{H264: &mediav1.H264Support{Profiles: []mediav1.H264Support_Profile{mediav1.H264Support_PROFILE_HIGH}, MaxLevel: 42}}}, Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_AAC, MaxChannels: 2}}}}, {Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MP4}}, Tracks: &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_Hevc{Hevc: &mediav1.HEVCSupport{Profiles: []mediav1.HEVCSupport_Profile{mediav1.HEVCSupport_PROFILE_MAIN_10}, BitDepths: []uint32{10}}}}, Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_AAC, MaxChannels: 2}}}}, {Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MPEGTS}}, Tracks: &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_H264{H264: &mediav1.H264Support{Profiles: []mediav1.H264Support_Profile{mediav1.H264Support_PROFILE_HIGH}, MaxLevel: 42}}}, Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_AAC, MaxChannels: 2}}}}, {Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MPEGTS}}, Tracks: &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_Hevc{Hevc: &mediav1.HEVCSupport{Profiles: []mediav1.HEVCSupport_Profile{mediav1.HEVCSupport_PROFILE_MAIN_10}, BitDepths: []uint32{10}}}}, Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_AAC, MaxChannels: 2}}}}}}
	seen := make(chan media.Capabilities, 1)
	c := serve(t, machinery(func(ctx context.Context, lent execute.Device, l output.Publisher, streams []*source.Stream, turns recovery.Turns) error {
		seen <- lent.Capabilities()
		return handoff(ctx, lent, l, streams, turns)
	}).backend())

	if err := castOn(t, c, streamOf("https://cdn.example/direct"), device).outcome(); err != nil {
		t.Fatal(err)
	}
	want := media.Capabilities{Deinterlaces: true, Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264, Profiles: []media.Profile{media.ProfileHigh}, MaxLevel: 42}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}, {Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecHEVC, Profiles: []media.Profile{media.ProfileMain10}, BitDepths: []int{10}}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}, {Transport: media.TransportMPEGTS, Video: media.VideoSupport{Codec: media.CodecH264, Profiles: []media.Profile{media.ProfileHigh}, MaxLevel: 42}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}, {Transport: media.TransportMPEGTS, Video: media.VideoSupport{Codec: media.CodecHEVC, Profiles: []media.Profile{media.ProfileMain10}, BitDepths: []int{10}}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}}
	if got := <-seen; !reflect.DeepEqual(got, want) {
		t.Errorf("the engine played on %+v, want %+v: what it knows, and nothing wider than the device said", got, want)
	}
}

func TestAStreamSourceIsMeasuredNotRanked(t *testing.T) {
	e := machinery(handoff)
	c := serve(t, e.backend())

	if err := castOn(t, c, streamOf("https://cdn.example/direct"), newLentDevice()).outcome(); err != nil {
		t.Fatal(err)
	}
	if got := <-e.asked; !proto.Equal(got, asked) {
		t.Errorf("the media server cast as asked %v, want %v", got, asked)
	}
	if measured := <-e.measured; measured.URL.String() != "https://cdn.example/direct" || measured.Headers.Get("Referer") != "https://page.example/" {
		t.Errorf("measured %v, want the stream with what fetching it needs", measured)
	}
}

func TestRankingAStreamSourceMeasuresItAsACastWould(t *testing.T) {
	e := machinery(nil)
	c := serve(t, e.backend())

	resp, err := c.streams.Rank(t.Context(), &mediav1.RankRequest{Source: streamOf("https://cdn.example/direct"), Settings: asked})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.GetRanked(); len(got) != 1 || got[0].GetUrl() != "https://cdn.example/direct" || got[0].GetLastResort() || got[0].Bitrate != nil {
		t.Errorf("ranked %v, want the stream alone, measured rather than ranked", got)
	}
	select {
	case measured := <-e.measured:
		if measured.Headers.Get("Referer") != "https://page.example/" {
			t.Errorf("measured %v without what fetching it needs", measured)
		}
	default:
		t.Error("ranking a stream source never measured it, so it can differ from what a cast plays")
	}
}

func TestADryRunIsTheServersRankingAsTheCastWouldAskIt(t *testing.T) {
	e := machinery(nil)
	c := serve(t, e.backend())

	resp, err := c.streams.Rank(t.Context(), &mediav1.RankRequest{Source: candidatesOf("https://cdn.example/a.m3u8", "https://cdn.example/b.m3u8"), Settings: asked})
	if err != nil {
		t.Fatal(err)
	}
	want := []*castorv1.RankedStream{{Url: "https://cdn.example/b.m3u8", LastResort: true}, {Url: "https://cdn.example/a.m3u8"}}
	if got := resp.GetRanked(); !slices.EqualFunc(got, want, func(a, b *castorv1.RankedStream) bool { return proto.Equal(a, b) }) {
		t.Errorf("ranked %+v, want the server's order %+v", got, want)
	}
	if got := <-e.asked; !proto.Equal(got, asked) {
		t.Errorf("ranked as asked %v, want %v", got, asked)
	}
}

// statusOf is cast id's status now, as a new watcher is first shown it.
func statusOf(t *testing.T, c server, id string) *castorv1.CastStatus {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := c.casts.Watch(ctx, &castorv1.WatchRequest{CastId: id})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() {
		t.Fatalf("the watch sent nothing: %v", stream.Err())
	}
	return stream.Msg().GetStatus()
}

func TestResolvedCandidatesWaitForTheirDeviceAlreadyMeasuring(t *testing.T) {
	c := serve(t, machinery(nil).backend())
	id := start(t, c, candidatesOf("https://cdn.example/ready.m3u8"))
	if got := statusOf(t, c, id); got.GetMeasuring() == nil || got.GetMeasuring().Castable != nil {
		t.Fatalf("resolved candidates showed %v while waiting for a device, want measuring", got)
	}
	if _, err := c.casts.Stop(t.Context(), &mediav1.StopRequest{CastId: id}); err != nil {
		t.Fatal(err)
	}
}

func TestAStreamCastNeverShowsExtractingAndItsStateNeverGoesBack(t *testing.T) {
	c := serve(t, machinery(func(ctx context.Context, device execute.Device, _ output.Publisher, streams []*source.Stream, turns recovery.Turns) error {
		turns.Attempting(1)
		turns.Revising(recovery.ServeInstead, "refused")
		turns.Attempting(2)
		return device.Play(ctx, streams[0].URL, streams[0].ContentType, nil)
	}).backend())

	w := castOn(t, c, streamOf("https://cdn.example/direct"), newLentDevice())
	if err := w.outcome(); err != nil {
		t.Fatal(err)
	}
	for i, s := range w.shown {
		if s.GetExtracting() != nil {
			t.Errorf("a cast of a stream showed %v", s)
		}
		if i > 0 && stateOrder(t, s) < stateOrder(t, w.shown[i-1]) {
			t.Errorf("the cast went back from %v to %v", w.shown[i-1], s)
		}
	}
	if last := w.shown[len(w.shown)-1]; last.GetCasting() == nil || last.GetCasting().GetAttempt() != 2 || last.GetCasting().GetRevision().GetAction() != castorv1.RecoveryAction_RECOVERY_ACTION_SERVE_INSTEAD || last.GetCasting().GetRevision().GetWhy() != "refused" {
		t.Errorf("the cast ended showing %v, want its last attempt casting", last)
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

func TestWhatTheServerServesTheDeviceFetchesFromTheServerItself(t *testing.T) {
	device := newLentDevice()
	delivered := make(chan string, 1)
	c := serve(t, machinery(func(ctx context.Context, lent execute.Device, publisher output.Publisher, _ []*source.Stream, _ recovery.Turns) error {
		pub, err := publisher.Publish(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			delivered <- r.URL.RequestURI()
			_, _ = io.WriteString(w, "media bytes")
		}))
		if err != nil {
			return err
		}
		defer pub.Close()
		if h := lent.Capabilities().ServedHeaders; h["transferMode.dlna.org"] != "Streaming" {
			return errors.New("the device's served headers did not reach the media server")
		}
		stream := pub.URL("stream.ts")
		stream.RawQuery = "n=1"
		return lent.Play(ctx, stream, media.MPEGTS, nil)
	}).backend())

	if err := castOn(t, c, streamOf("https://cdn.example/direct"), device).outcome(); err != nil {
		t.Fatal(err)
	}
	handed := <-device.handed
	if "http://"+handed.Host != c.media || !strings.HasPrefix(handed.Path, "/media/") {
		t.Errorf("the device was handed %s, want the server's media route at %s", handed, c.media)
	}
	if as := <-device.as; as != mediav1.Container_CONTAINER_MPEGTS {
		t.Errorf("the device was handed the stream as %v, want MPEG-TS as the engine served it", as)
	}
	if got := string(<-device.played); got != "media bytes" {
		t.Errorf("the device fetched %q, want the served bytes", got)
	}
	if got := <-delivered; got != "/stream.ts?n=1" {
		t.Errorf("the delivery was asked for %q, want the path and query the engine served", got)
	}
	closed, err := http.Get(c.media + handed.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	closed.Body.Close()
	if closed.StatusCode != http.StatusNotFound {
		t.Errorf("finished delivery answered %d, want 404", closed.StatusCode)
	}
}

func TestASourceOnLoopbackIsHandedToTheDeviceAsItIs(t *testing.T) {
	device := newLentDevice()
	c := serve(t, machinery(handoff).backend())

	if err := castOn(t, c, streamOf("http://127.0.0.1:9/movie.mp4"), device).outcome(); err == nil {
		t.Fatal("the device fetched an origin nothing serves")
	}
	if got := (<-device.handed).String(); got != "http://127.0.0.1:9/movie.mp4" {
		t.Errorf("the device was handed %s, want the source itself: only what the cast serves goes through the server", got)
	}
}

func TestDevicesReachOnlyPublishedDeliveriesNeverTheAPI(t *testing.T) {
	released := make(chan struct{})
	c := serve(t, machinery(func(context.Context, execute.Device, output.Publisher, []*source.Stream, recovery.Turns) error {
		<-released
		return nil
	}).backend())

	id := start(t, c, streamOf("https://cdn.example/direct"))
	w := watching(t, c, id)
	go func() { _ = lend(t.Context(), c, id, newLentDevice()) }()
	for what, path := range map[string]string{
		"an unpublished delivery": "/media/unknown/etc/passwd",
		"the API":                 "/" + mediav1connect.CastServiceName + "/Stop",
	} {
		resp, err := http.Post(c.media+path, "application/json", strings.NewReader(`{"castId":"`+id+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("devices reaching %s were answered %d, want 404", what, resp.StatusCode)
		}
	}
	close(released)
	if err := (<-w).outcome(); err != nil {
		t.Fatal(err)
	}
}

func TestADeviceGoneOnItsNetworkIsGoneToTheCastsRecovery(t *testing.T) {
	device := newLentDevice()
	device.end = &mediav1.DeviceError{Error: &mediav1.DeviceError_Gone_{Gone: &mediav1.DeviceError_Gone{Device: "Bedroom", Observed: "stopped answering", Cause: "connection refused"}}}
	seen := make(chan error, 1)
	c := serve(t, machinery(func(ctx context.Context, lent execute.Device, _ output.Publisher, _ []*source.Stream, _ recovery.Turns) error {
		err := lent.AwaitEnd(ctx)
		seen <- err
		return err
	}).backend())

	w := castOn(t, c, streamOf("https://cdn.example/direct"), device)
	err := w.outcome()
	if gone, ok := errors.AsType[*media.Gone](<-seen); !ok || gone.Device != "Bedroom" || gone.Observed != "stopped answering" || gone.Err == nil || gone.Err.Error() != "connection refused" {
		t.Errorf("the engine saw %v, want a *media.Gone it can classify, with how and why", gone)
	}
	if err == nil || !strings.Contains(err.Error(), "unreachable") || w.ended.GetFailed().GetCode() != castorv1.FailureCode_FAILURE_CODE_DEVICE_UNREACHABLE {
		t.Errorf("the cast ended with %v, want the device's loss", err)
	}
}

func TestACastHasOneDeviceAndPlaysOnWhenItsLenderLeaves(t *testing.T) {
	playing, ended := make(chan struct{}), make(chan struct{})
	c := serve(t, machinery(func(ctx context.Context, device execute.Device, _ output.Publisher, streams []*source.Stream, _ recovery.Turns) error {
		if err := device.Play(ctx, streams[0].URL, streams[0].ContentType, nil); err != nil {
			return err
		}
		close(playing)
		err := device.AwaitEnd(ctx)
		close(ended)
		return err
	}).backend())

	id := start(t, c, streamOf("https://cdn.example/direct"))
	w := watching(t, c, id)
	lending, leave := context.WithCancel(t.Context())
	go func() { _ = lend(lending, c, id, newLentDevice()) }()
	<-playing

	if err := lend(t.Context(), c, id, newLentDevice()); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a second device was met with %v, want it refused", err)
	}
	leave()
	select {
	case <-ended:
		t.Fatal("the cast ended with the lender of its device")
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := c.casts.Stop(t.Context(), &mediav1.StopRequest{CastId: id}); err != nil {
		t.Fatal(err)
	}
	if got := <-w; got.ended.GetStopped() == nil {
		t.Errorf("the cast ended %v, want the stop that ended the cast its lender had left", got.ended)
	}
}

func TestACastThatNeedsItsDeviceAgainOnceItsLenderLeftEndsThere(t *testing.T) {
	lent, again := make(chan struct{}), make(chan struct{})
	c := serve(t, machinery(func(ctx context.Context, device execute.Device, _ output.Publisher, streams []*source.Stream, _ recovery.Turns) error {
		close(lent)
		<-again
		// Recovery revising the attempt, each revision playing again, until the cast is ended.
		for range 50 {
			if err := device.Play(ctx, streams[0].URL, streams[0].ContentType, nil); ctx.Err() != nil {
				return err
			}
		}
		return errors.New("recovery revised a cast whose device can no longer be told anything")
	}).backend())

	id := start(t, c, streamOf("https://cdn.example/direct"))
	w := watching(t, c, id)
	lending, leave := context.WithCancel(t.Context())
	go func() { _ = lend(lending, c, id, newLentDevice()) }()
	<-lent
	leave()
	close(again)
	if got := <-w; got.outcome() == nil || !strings.Contains(got.outcome().Error(), "left") || got.ended.GetFailed().GetCode() != castorv1.FailureCode_FAILURE_CODE_DEVICE_UNREACHABLE {
		t.Errorf("the cast ended with %v, want it failed for its lender leaving", got.ended)
	}
}

func TestWatchingNeverDrivesAndTheCastStartsWithItsLender(t *testing.T) {
	device := newLentDevice()
	c := serve(t, machinery(handoff).backend())

	id := start(t, c, streamOf("https://cdn.example/direct"))
	w := watching(t, c, id)
	onlooker := watching(t, c, id)
	select {
	case <-device.handed:
		t.Fatal("watching the cast played on a device")
	case <-time.After(200 * time.Millisecond):
	}
	if err := lend(t.Context(), c, id, device); err != nil {
		t.Fatal(err)
	}
	if err := (<-w).outcome(); err != nil {
		t.Errorf("the watch ended with %v, want the cast's clean end", err)
	}
	if err := (<-onlooker).outcome(); err != nil {
		t.Errorf("a second watcher ended with %v, want the same clean end", err)
	}
	if got := string(<-device.played); got != "https://cdn.example/direct" {
		t.Errorf("played %q", got)
	}
}

func TestCandidateMetadataReachesRankingWhole(t *testing.T) {
	e := machinery(nil)
	c := serve(t, e.backend())
	input := candidatesOf("https://cdn.example/master.m3u8", "https://cdn.example/720.m3u8")
	for _, stream := range input.GetStreams().GetStreams() {
		stream.Stream.Headers = map[string]string{"Referer": "https://site.example/watch"}
		stream.Stream.ContentType = "application/vnd.apple.mpegurl"
		stream.SourcePage = "https://site.example/watch"
	}
	input.GetStreams().Streams[0].Ladder = castorv1.Ladder_LADDER_MULTIVARIANT

	if _, err := c.streams.Rank(t.Context(), &mediav1.RankRequest{Source: input, Settings: asked}); err != nil {
		t.Fatal(err)
	}
	ranked := <-e.ranked
	if len(ranked) != 2 || ranked[0].Ladder != source.LadderMultivariant {
		t.Fatalf("ranking was handed %v, want the master with the ladder the browser read", ranked)
	}
	if ranked[1].Headers.Get("Referer") != "https://site.example/watch" || ranked[1].ContentType != "application/vnd.apple.mpegurl" {
		t.Errorf("a stream reached ranking as %+v, without what fetching it needs", ranked[1])
	}
}

func TestMalformedSourcesAreRejectedBeforeRankingOrPlayback(t *testing.T) {
	e := machinery(handoff)
	c := serve(t, e.backend())
	if _, err := c.casts.Start(t.Context(), &mediav1.StartRequest{CastId: castid.New(), Settings: asked}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("Start answered %v, want invalid argument", err)
	}
	if _, err := c.streams.Rank(t.Context(), &mediav1.RankRequest{Settings: asked}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("Rank answered %v, want invalid argument", err)
	}
	select {
	case settings := <-e.asked:
		t.Errorf("a malformed source reached the engine: %v", settings)
	default:
	}
}

func TestAServerShuttingDownFailsItsCastsSayingSoAndTakesNoMore(t *testing.T) {
	playing := make(chan struct{})
	var tornDown atomic.Bool
	c := serve(t, machinery(func(ctx context.Context, _ execute.Device, _ output.Publisher, _ []*source.Stream, _ recovery.Turns) error {
		close(playing)
		<-ctx.Done()
		// Teardown that takes a while, which shutting down waits for.
		time.Sleep(100 * time.Millisecond)
		tornDown.Store(true)
		return ctx.Err()
	}).backend())

	id := start(t, c, streamOf("https://cdn.example/direct"))
	w := watching(t, c, id)
	go func() { _ = lend(t.Context(), c, id, newLentDevice()) }()
	<-playing
	c.running.Shutdown(t.Context())
	if !tornDown.Load() {
		t.Error("shutting down returned before its cast had let go of what it held")
	}
	if got := <-w; got.ended.GetFailed().GetCode() != castorv1.FailureCode_FAILURE_CODE_SERVER_SHUTDOWN || got.ended.GetFailed().GetMessage() != "server shutting down" {
		t.Errorf("the cast ended %v, want it failed as the server shut down", got.ended)
	}
	if _, err := c.casts.Start(t.Context(), &mediav1.StartRequest{CastId: castid.New(), Source: streamOf("https://cdn.example/direct"), Settings: asked}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("a cast started on a server shutting down was met with %v, want unavailable", err)
	}
}

func TestACastIDIsHeldToTheContractAndToOneCastAtATime(t *testing.T) {
	c := serve(t, machinery(handoff).backend())

	for _, id := range []string{"", "short", "lowercase-and-far-too-long-for-an-id"} {
		_, err := c.casts.Start(t.Context(), &mediav1.StartRequest{CastId: id, Source: streamOf("https://cdn.example/direct"), Settings: asked})
		if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "cast_id") {
			t.Errorf("Start with id %q answered %v, want invalid argument naming cast_id", id, err)
		}
	}
	id := start(t, c, streamOf("https://cdn.example/direct"))
	if _, err := c.casts.Start(t.Context(), &mediav1.StartRequest{CastId: id, Source: streamOf("https://cdn.example/other"), Settings: asked}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("a second Start under id %s answered %v, want already exists", id, err)
	}
}
