// The drive and the watch against the media server's contract, with a fake device lent between them.
package mediaclient_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/gen/castor/media/v1/mediav1connect"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/mediaclient"
)

// media is the media server at its contract: its one cast plays commands on the lent device, each once the last is answered, then ends.
type media struct {
	commands []*mediav1.DeviceCommand
	// lent is the capabilities the cast was lent its device with.
	lent chan *mediav1.Capabilities
	// answered is every answer, in order.
	answered chan *mediav1.AnswerRequest
	answers  chan *mediav1.AnswerRequest
	driven   chan struct{}
}

const castID = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

func (*media) Start(context.Context, *mediav1.StartRequest) (*mediav1.StartResponse, error) {
	return &mediav1.StartResponse{}, nil
}

func (*media) Stop(context.Context, *mediav1.StopRequest) (*mediav1.StopResponse, error) {
	return &mediav1.StopResponse{}, nil
}

// Watch shows the cast casting, then how its device answered: ended, or failed by the device.
func (m *media) Watch(ctx context.Context, _ *castorv1.WatchRequest, out *connect.ServerStream[castorv1.WatchResponse]) error {
	if err := out.Send(&castorv1.WatchResponse{Update: &castorv1.WatchResponse_Status{Status: &castorv1.CastStatus{State: &castorv1.CastStatus_Casting{Casting: &castorv1.CastingStatus{Attempt: 1}}}}}); err != nil {
		return err
	}
	select {
	case <-m.driven:
	case <-ctx.Done():
		return ctx.Err()
	}
	return out.Send(&castorv1.WatchResponse{Update: &castorv1.WatchResponse_Ended{Ended: &castorv1.Ended{Result: &castorv1.Ended_Completed{Completed: &emptypb.Empty{}}}}})
}

func (m *media) Drive(ctx context.Context, req *mediav1.DriveRequest, out *connect.ServerStream[mediav1.DriveResponse]) error {
	defer close(m.driven)
	m.lent <- req.GetCapabilities()
	for _, cmd := range m.commands {
		if err := out.Send(&mediav1.DriveResponse{Command: cmd}); err != nil {
			return err
		}
		select {
		case a := <-m.answers:
			m.answered <- a
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (m *media) Answer(_ context.Context, req *mediav1.AnswerRequest) (*mediav1.AnswerResponse, error) {
	m.answers <- req
	return &mediav1.AnswerResponse{}, nil
}

func play(raw string, container mediav1.Container) *mediav1.DeviceCommand {
	return &mediav1.DeviceCommand{Command: &mediav1.DeviceCommand_Call_{Call: &mediav1.DeviceCommand_Call{Id: "play", Operation: &mediav1.DeviceCommand_Call_Play{Play: &mediav1.DeviceCommand_Play{Url: raw, Container: container}}}}}
}

func serve(t *testing.T, commands ...*mediav1.DeviceCommand) (*mediaclient.Client, *media) {
	t.Helper()
	m := &media{commands: commands, lent: make(chan *mediav1.Capabilities, 1), answered: make(chan *mediav1.AnswerRequest, len(commands)), answers: make(chan *mediav1.AnswerRequest, 1), driven: make(chan struct{})}
	// Every message is held to the rules the contract states, as the media server holds them.
	valid := connect.WithInterceptors(validate.NewInterceptor(validate.WithValidateResponses()))
	mux := http.NewServeMux()
	mux.Handle(mediav1connect.NewCastServiceHandler(m, valid))
	mux.Handle(mediav1connect.NewDeviceServiceHandler(m, valid))
	api := httptest.NewTestServer(t, mux)
	return mediaclient.New(api.Client(), api.URL), m
}

// lentDevice plays what it is handed, or refuses it with refusal.
type lentDevice struct {
	played  chan *url.URL
	as      chan mediav1.Container
	info    chan *castorv1.MediaInfo
	refusal error
}

func newLentDevice() *lentDevice {
	return &lentDevice{played: make(chan *url.URL, 1), as: make(chan mediav1.Container, 1), info: make(chan *castorv1.MediaInfo, 1)}
}

func (s *lentDevice) Play(_ context.Context, playback device.Playback) error {
	if s.refusal != nil {
		return s.refusal
	}
	s.played <- playback.URL
	s.as <- playback.Container
	s.info <- playback.Info
	return nil
}

func (*lentDevice) AwaitEnd(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (*lentDevice) Capabilities() *mediav1.Capabilities {
	return &mediav1.Capabilities{SelfFetch: true, Formats: []*mediav1.FormatSupport{{Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MP4}}, Tracks: &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_H264{H264: &mediav1.H264Support{MaxLevel: 42}}}, Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_AAC}}}}}}
}

var asked = &mediav1.PlaybackSettings{Delivery: castorv1.Delivery_DELIVERY_AUTO, MaxHeight: 1080, Subtitles: &castorv1.SubtitleSelection{Mode: &castorv1.SubtitleSelection_Disabled{Disabled: &emptypb.Empty{}}}}

// watching is what a watch of a cast was shown, and how it ended.
type watching struct {
	shown []*castorv1.CastStatus
	ended *castorv1.Ended
}

// cast starts a cast of one stream, lends it device and watches it to its end, returning what the watch saw and what the drive returned.
func cast(t *testing.T, c *mediaclient.Client, lent device.Device) (watching, error) {
	return castWithInfo(t, c, lent, nil)
}

func castWithInfo(t *testing.T, c *mediaclient.Client, lent device.Device, info *castorv1.MediaInfo) (watching, error) {
	t.Helper()
	err := c.Start(t.Context(), castID, &mediav1.Source{Source: &mediav1.Source_Stream{Stream: &castorv1.Stream{Url: "https://cdn.example/direct"}}}, asked, 0)
	if err != nil {
		t.Fatal(err)
	}
	driven := make(chan error, 1)
	go func() { driven <- c.Drive(t.Context(), castID, lent, info) }()
	var w watching
	w.ended, err = c.Watch(t.Context(), castID, func(s *castorv1.CastStatus) { w.shown = append(w.shown, s) })
	if err != nil {
		t.Fatal(err)
	}
	return w, <-driven
}

func TestDriveHandsCastMetadataToTheDevice(t *testing.T) {
	c, _ := serve(t, play("https://cdn.example/direct", mediav1.Container_CONTAINER_HLS))
	lent := newLentDevice()
	info := &castorv1.MediaInfo{Title: "The Matrix", ImageUrl: "https://image.tmdb.org/t/p/w500/matrix.jpg"}
	if _, err := castWithInfo(t, c, lent, info); err != nil {
		t.Fatal(err)
	}
	if got := taken(t, lent.info); !proto.Equal(got, info) {
		t.Errorf("device info = %+v, want %+v", got, info)
	}
}

// taken is what ch already holds, failing t when it holds nothing: once a cast has ended, nothing more comes.
func taken[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	default:
		var zero T
		t.Fatalf("nothing came on %T before the cast ended", ch)
		return zero
	}
}

func TestADriveLendsItsDeviceForTheCastToPlayOn(t *testing.T) {
	c, m := serve(t, play("https://cdn.example/direct", mediav1.Container_CONTAINER_HLS))
	lent := newLentDevice()

	w, driven := cast(t, c, lent)
	if driven != nil {
		t.Errorf("the drive ended with %v", driven)
	}
	if w.ended.GetCompleted() == nil || len(w.shown) != 1 || w.shown[0].GetCasting().GetAttempt() != 1 {
		t.Errorf("the watch was shown %v then %v, want the cast's status then its end", w.shown, w.ended)
	}
	if got, as := taken(t, lent.played), taken(t, lent.as); got.String() != "https://cdn.example/direct" || as != mediav1.Container_CONTAINER_HLS {
		t.Errorf("the device played %s as %v, want the stream as HLS", got, as)
	}
	if got := taken(t, m.answered); got.GetDone() == nil || got.GetCommandId() != "play" || got.GetCastId() != castID {
		t.Errorf("the play was answered %v, want it done", got)
	}
	if got := taken(t, m.lent); !proto.Equal(got, lent.Capabilities()) {
		t.Errorf("the cast was lent a device with %v, want the device's own capabilities", got)
	}
}

func TestADeviceFailureWithoutAMessageFailsThePlayNotTheDrive(t *testing.T) {
	c, m := serve(t, play("https://cdn.example/direct", mediav1.Container_CONTAINER_HLS))
	lent := newLentDevice()
	lent.refusal = errors.New("")

	if _, driven := cast(t, c, lent); driven != nil {
		t.Errorf("the drive ended with %v, want it to have carried the failure", driven)
	}
	if got := taken(t, m.answered); got.GetError().GetMessage() == "" {
		t.Errorf("the play was answered %v, want the device's failure", got)
	}
}

func TestADeviceGoneIsAnsweredGoneSoTheCastsRecoveryReadsIt(t *testing.T) {
	c, m := serve(t, play("https://cdn.example/direct", mediav1.Container_CONTAINER_HLS))
	lent := newLentDevice()
	lent.refusal = &device.Gone{Device: "Bedroom", Observed: "stopped answering", Err: errors.New("connection refused")}

	if _, driven := cast(t, c, lent); driven != nil {
		t.Errorf("the drive ended with %v", driven)
	}
	want := &mediav1.DeviceError_Gone{Device: "Bedroom", Observed: "stopped answering", Cause: "connection refused"}
	if got := taken(t, m.answered).GetError().GetGone(); !proto.Equal(got, want) {
		t.Errorf("the play was answered gone as %v, want %v", got, want)
	}
}

func (*lentDevice) Stop(context.Context) error   { return nil }
func (*lentDevice) Close() error                 { return nil }
func (*lentDevice) Pause(context.Context) error  { return nil }
func (*lentDevice) Resume(context.Context) error { return nil }
