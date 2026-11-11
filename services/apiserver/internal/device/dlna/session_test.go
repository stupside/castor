package dlna

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"

	"github.com/huin/goupnp"
	"github.com/huin/goupnp/soap"

	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/device/devicetest"
)

type actionTransport func(*http.Request) (*http.Response, error)

func (f actionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAPlayTimeoutNeedsConfirmationOfTheRequestedStream(t *testing.T) {
	const target = "http://192.0.2.1:8080/media/stream"
	for _, tt := range []struct {
		name, state, uri string
		play, query      string
		cancelAt         string
		want             bool
	}{
		{name: "acknowledged", want: true},
		{name: "playing despite timeout", play: "timeout", state: "PLAYING", uri: target, want: true},
		{name: "still stopped", play: "timeout", state: "STOPPED", uri: target},
		{name: "still buffering", play: "timeout", state: "TRANSITIONING", uri: target},
		{name: "playing another stream", play: "timeout", state: "PLAYING", uri: target + "-old"},
		{name: "state unanswered", play: "timeout", query: "GetTransportInfo"},
		{name: "URI unanswered", play: "timeout", state: "PLAYING", query: "GetMediaInfo"},
		{name: "explicit refusal", play: "refused", state: "PLAYING", uri: target},
		{name: "SOAP refusal", play: "fault", state: "PLAYING", uri: target},
		{name: "cancelled", play: "timeout", state: "PLAYING", uri: target, cancelAt: "Play"},
		{name: "cancelled during confirmation", play: "timeout", state: "PLAYING", query: "GetMediaInfo", cancelAt: "GetMediaInfo"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var actions []string
				client := soap.NewSOAPClient(url.URL{Scheme: "http", Host: "192.0.2.10", Path: "/control"})
				client.HTTPClient.Transport = actionTransport(func(r *http.Request) (*http.Response, error) {
					// A fake transport sees goupnp's headers before net/http canonicalizes them.
					var soapAction string
					for name, values := range r.Header {
						if strings.EqualFold(name, "SOAPAction") && len(values) > 0 {
							soapAction = values[0]
						}
					}
					action := strings.Split(strings.Trim(soapAction, `"`), "#")[1]
					actions = append(actions, action)
					if action == "Play" && tt.play == "timeout" || action == tt.query {
						if tt.cancelAt == action {
							cancel()
						}
						<-r.Context().Done()
						return nil, r.Context().Err()
					}
					if action == "Play" && tt.play == "refused" {
						return nil, errors.New("device refused Play")
					}
					if action == "Play" && tt.play == "fault" {
						body := `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>701</errorCode><errorDescription>Transition not available</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`
						return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
					}
					var body string
					switch action {
					case "GetTransportInfo":
						body = "<CurrentTransportState>" + tt.state + "</CurrentTransportState>"
					case "GetMediaInfo":
						body = "<CurrentURI>" + tt.uri + "</CurrentURI>"
					}
					body = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:` + action +
						`Response xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` + body + `</u:` + action + `Response></s:Body></s:Envelope>`
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
				})
				_, sinks := parseSinkProtocolInfo("http-get:*:video/mp2t:*")
				s := &session{transport: goupnp.ServiceClient{
					SOAPClient: client,
					Service:    &goupnp.Service{ServiceType: "urn:schemas-upnp-org:service:AVTransport:1"},
				}, sinks: sinks}
				u, _ := url.Parse(target)
				started := time.Now()
				err := s.Play(ctx, device.Playback{URL: u, Play: devicetest.Source(mediav1.Container_CONTAINER_MPEGTS)})
				if (err == nil) != tt.want {
					t.Fatalf("Play() = %v, want success %v; actions %v", err, tt.want, actions)
				}
				if tt.cancelAt != "" && !errors.Is(err, context.Canceled) {
					t.Fatalf("Play() = %v, want cancellation", err)
				}
				if tt.play == "fault" {
					if _, ok := errors.AsType[*soap.SOAPFaultError](err); !ok {
						t.Fatalf("Play() = %v, want preserved SOAP fault", err)
					}
				}
				if got := time.Since(started); got > 2*actionTimeout {
					t.Errorf("Play took %v, want at most two bounded actions", got)
				}
				plays, queries := 0, 0
				for _, action := range actions {
					if action == "Play" {
						plays++
					}
					if strings.HasPrefix(action, "Get") {
						queries++
					}
				}
				if plays != 1 {
					t.Errorf("sent Play %d times, want once", plays)
				}
				if (tt.play == "" || tt.play == "refused" || tt.play == "fault" || tt.cancelAt == "Play") && queries != 0 {
					t.Errorf("queried %d times without an ambiguous timeout", queries)
				}
			})
		})
	}
}

func TestTheTransportStateEndsTheCastOnlyOncePlaybackBegan(t *testing.T) {
	for _, tt := range []struct {
		name   string
		states []transportState
		want   bool
	}{
		{"stop before playback is ignored", []transportState{"STOPPED", "TRANSITIONING", "STOPPED"}, false},
		{"stop after playing ends", []transportState{"PLAYING", "STOPPED"}, true},
		{"pause remains active", []transportState{"PLAYING", "PAUSED_PLAYBACK"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var watch transportWatch
			got := false
			for _, state := range tt.states {
				got = watch.observe(state)
			}
			if got != tt.want {
				t.Errorf("observe(%v) = %v, want %v", tt.states, got, tt.want)
			}
		})
	}
}

func TestAnUnbrokenRunOfFailedPollsIsNeededToEndTheCast(t *testing.T) {
	type answer = struct {
		info transportInfo
		err  error
	}
	gone := errors.New("connect: connection refused")
	var script []answer
	for range 2 {
		for range int(device.UnreachableWindow/device.PollInterval) - 1 {
			script = append(script, answer{err: gone})
		}
		script = append(script, answer{info: transportInfo{CurrentTransportState: "PLAYING"}})
	}
	script[len(script)-1].info.CurrentTransportState = "STOPPED"

	synctest.Test(t, func(t *testing.T) {
		calls := 0
		poll := func(context.Context) (transportInfo, error) {
			a := script[min(calls, len(script)-1)]
			calls++
			return a.info, a.err
		}
		if err := awaitTransportEnd(t.Context(), "Living Room", poll); err != nil {
			t.Fatalf("awaitTransportEnd() = %v, want nil: the device answered between the failures", err)
		}
		if calls != len(script) {
			t.Errorf("polled %d times, want %d", calls, len(script))
		}
	})
}

// playingDevice answers every GetTransportInfo with PLAYING, counting the polls.
type playingDevice struct{ polls atomic.Int32 }

func (r *playingDevice) RoundTrip(*http.Request) (*http.Response, error) {
	r.polls.Add(1)
	body := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body>` +
		`<u:GetTransportInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` +
		`<CurrentTransportState>PLAYING</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus><CurrentSpeed>1</CurrentSpeed>` +
		`</u:GetTransportInfoResponse></s:Body></s:Envelope>`
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
}

func TestDLNAAnswersWhenTheCastEnds(t *testing.T) {
	dev := &playingDevice{}
	devicetest.AwaitsTheCastsEnd(t, func() device.Device {
		client := soap.NewSOAPClient(url.URL{Scheme: "http", Host: "192.0.2.10:2870", Path: "/control"})
		client.HTTPClient.Transport = dev
		return &session{transport: goupnp.ServiceClient{
			SOAPClient: client,
			Service:    &goupnp.Service{ServiceType: "urn:schemas-upnp-org:service:AVTransport:1"},
		}}
	})
	if dev.polls.Load() == 0 {
		t.Error("the suite never polled the device")
	}
}

func TestADeviceThatStaysStoppedNeverPlayedTheStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		polls := 0
		poll := func(context.Context) (transportInfo, error) {
			polls++
			return transportInfo{CurrentTransportState: "STOPPED"}, nil
		}
		err := awaitTransportEnd(t.Context(), "Living Room", poll)
		if err == nil || !strings.Contains(err.Error(), "refused the stream") {
			t.Fatalf("awaitTransportEnd() = %v, want the device named as having refused the stream", err)
		}
		if polls != idlePolls {
			t.Errorf("polled %d times, want %d", polls, idlePolls)
		}
	})
}

func TestADeviceBufferingBeforeItPlaysIsNotRefusing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		script := []transportState{"STOPPED", "STOPPED", "TRANSITIONING", "STOPPED", "PLAYING", "STOPPED"}
		i := 0
		poll := func(context.Context) (transportInfo, error) {
			state := script[i]
			i++
			return transportInfo{CurrentTransportState: state}, nil
		}
		if err := awaitTransportEnd(t.Context(), "Living Room", poll); err != nil || i != len(script) {
			t.Fatalf("awaitTransportEnd() = %v after %d polls, want a clean end once it played and stopped", err, i)
		}
	})
}

// renderer is a device answering each AVTransport action with the elements answer gives it.
func renderer(answer func(action string) string) *session {
	client := soap.NewSOAPClient(url.URL{Scheme: "http", Host: "192.0.2.10:2870", Path: "/control"})
	client.HTTPClient.Transport = actionTransport(func(r *http.Request) (*http.Response, error) {
		var action string
		for name, values := range r.Header {
			if strings.EqualFold(name, "SOAPAction") && len(values) > 0 {
				action = strings.Split(strings.Trim(values[0], `"`), "#")[1]
			}
		}
		body := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:` + action + `Response xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` +
			answer(action) + `</u:` + action + `Response></s:Body></s:Envelope>`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
	})
	return &session{transport: goupnp.ServiceClient{SOAPClient: client, Service: &goupnp.Service{ServiceType: "urn:schemas-upnp-org:service:AVTransport:1"}}}
}

func TestADeviceThatStopsWithAnErrorFailsTheCast(t *testing.T) {
	for _, tt := range []struct {
		name   string
		script []string
		fails  bool
	}{
		{"stopped after playing", []string{"PLAYING OK", "STOPPED OK"}, false},
		{"stopped with an error after playing", []string{"PLAYING OK", "STOPPED ERROR_OCCURRED"}, true},
		{"an error left from an earlier playback", []string{"STOPPED ERROR_OCCURRED", "PLAYING OK", "STOPPED OK"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				polls := 0
				dev := renderer(func(string) string {
					state, status, _ := strings.Cut(tt.script[min(polls, len(tt.script)-1)], " ")
					polls++
					return "<CurrentTransportState>" + state + "</CurrentTransportState><CurrentTransportStatus>" + status + "</CurrentTransportStatus>"
				})
				err := dev.AwaitEnd(t.Context())
				if _, failed := errors.AsType[*device.PlaybackError](err); failed != tt.fails || !failed && err != nil {
					t.Fatalf("AwaitEnd = %v, want a playback error %v", err, tt.fails)
				}
				if polls != len(tt.script) {
					t.Errorf("polled %d times, want %d", polls, len(tt.script))
				}
			})
		})
	}
}

func TestADevicePositionSaysWhetherItsViewerHoldsIt(t *testing.T) {
	for state, paused := range map[string]bool{"PLAYING": false, "PAUSED_PLAYBACK": true} {
		dev := renderer(func(action string) string {
			if action == "GetPositionInfo" {
				return "<RelTime>0:01:30</RelTime>"
			}
			return "<CurrentTransportState>" + state + "</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus>"
		})
		want := device.Playhead{At: 90 * time.Second, Paused: paused}
		if got, err := dev.Position(t.Context()); err != nil || got != want {
			t.Errorf("Position while %s = %v, %v; want %v", state, got, err, want)
		}
	}
}

func TestARendererThatTakesANewStreamOnlyOnceStoppedIsStoppedFirst(t *testing.T) {
	var actions []string
	stopped := false
	client := soap.NewSOAPClient(url.URL{Scheme: "http", Host: "192.0.2.10", Path: "/control"})
	client.HTTPClient.Transport = actionTransport(func(r *http.Request) (*http.Response, error) {
		var action string
		for name, values := range r.Header {
			if strings.EqualFold(name, "SOAPAction") && len(values) > 0 {
				action = strings.Split(strings.Trim(values[0], `"`), "#")[1]
			}
		}
		actions = append(actions, action)
		stopped = stopped || action == "Stop"
		if action == "SetAVTransportURI" && !stopped {
			// LG's answer to a new stream while it holds a paused one.
			body := `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>701</errorCode><errorDescription>Transition not available</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`
			return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
		}
		body := `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:` + action + `Response xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"></u:` + action + `Response></s:Body></s:Envelope>`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	_, sinks := parseSinkProtocolInfo("http-get:*:video/mp2t:*")
	s := &session{transport: goupnp.ServiceClient{SOAPClient: client, Service: &goupnp.Service{ServiceType: "urn:schemas-upnp-org:service:AVTransport:1"}}, sinks: sinks}
	u, _ := url.Parse("http://192.0.2.1:8080/media/stream")
	if err := s.Play(t.Context(), device.Playback{URL: u, Play: devicetest.Source(mediav1.Container_CONTAINER_MPEGTS)}); err != nil {
		t.Fatalf("Play() = %v; actions %v", err, actions)
	}
	if want := []string{"SetAVTransportURI", "Stop", "SetAVTransportURI", "Play"}; !slices.Equal(actions, want) {
		t.Fatalf("actions %v, want %v", actions, want)
	}
}

func TestAStopRefusedWithNothingLoadedHasNothingToStop(t *testing.T) {
	for state, refused := range map[string]bool{"NO_MEDIA_PRESENT": false, "STOPPED": false, "PLAYING": true} {
		client := soap.NewSOAPClient(url.URL{Scheme: "http", Host: "192.0.2.10", Path: "/control"})
		client.HTTPClient.Transport = actionTransport(func(r *http.Request) (*http.Response, error) {
			stop := false
			for name, values := range r.Header {
				stop = stop || strings.EqualFold(name, "SOAPAction") && len(values) > 0 && strings.HasSuffix(strings.Trim(values[0], `"`), "#Stop")
			}
			if stop {
				// LG's answer to Stop while it holds no stream.
				body := `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>701</errorCode><errorDescription>Transition not available</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`
				return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
			}
			body := `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetTransportInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><CurrentTransportState>` + state + `</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus></u:GetTransportInfoResponse></s:Body></s:Envelope>`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		s := &session{transport: goupnp.ServiceClient{SOAPClient: client, Service: &goupnp.Service{ServiceType: "urn:schemas-upnp-org:service:AVTransport:1"}}}
		if err := s.Stop(t.Context()); (err != nil) != refused || refused && !upnpError(err, transitionUnavailable) {
			t.Errorf("Stop while %s = %v, want a refusal %v", state, err, refused)
		}
	}
}

func TestACancelWhileTheTransportStaysLockedCarriesItsCause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cause := errors.New("viewer stopped")
		ctx, cancel := context.WithCancelCause(t.Context())
		locked := &soap.SOAPFaultError{}
		locked.Detail.UPnPError.Errorcode = transportLocked
		err := retryTransportLocked(ctx, func() error { cancel(cause); return locked })
		if !errors.Is(err, cause) {
			t.Errorf("a cancel while the transport stayed locked ended with %v, want its cause", err)
		}
	})
}

func TestAPlayheadAskedOfTheStreamBeforeAPlayDoesNotTickTheNewOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rel := "0:00:42"
		held := make(chan struct{})
		var holding atomic.Bool
		holding.Store(true)
		s := renderer(func(action string) string {
			switch action {
			case "GetPositionInfo":
				return "<RelTime>" + rel + "</RelTime>"
			case "GetTransportInfo":
				if holding.CompareAndSwap(true, false) {
					<-held
				}
				return "<CurrentTransportState>PLAYING</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus>"
			}
			return ""
		})
		_, s.sinks = parseSinkProtocolInfo("http-get:*:video/mp2t:*")
		u, _ := url.Parse("http://192.0.2.1:8080/media/first")
		_ = s.Play(t.Context(), device.Playback{URL: u, Play: devicetest.Source(mediav1.Container_CONTAINER_MPEGTS)})
		asked := make(chan struct{})
		go func() { _, _ = s.Position(t.Context()); close(asked) }()
		synctest.Wait()
		u, _ = url.Parse("http://192.0.2.1:8080/media/second")
		if err := s.Play(t.Context(), device.Playback{URL: u, Play: devicetest.Source(mediav1.Container_CONTAINER_MPEGTS)}); err != nil {
			t.Fatal(err)
		}
		close(held)
		<-asked
		rel = "0:00:00"
		if head, err := s.Position(t.Context()); !errors.Is(err, device.ErrNoPosition) {
			t.Errorf("a 0:00:00 of the second stream = %v, %v; want no position: the tick seen belonged to the first", head, err)
		}
	})
}

func TestAPlayheadOfTheStreamPlayedUntilPlayLandsDoesNotTickTheNewOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		track, rel := "http://192.0.2.1:8080/media/first", "0:00:42"
		s := renderer(func(action string) string {
			switch action {
			case "GetPositionInfo":
				return "<TrackURI>" + track + "</TrackURI><RelTime>" + rel + "</RelTime>"
			case "GetTransportInfo":
				return "<CurrentTransportState>PLAYING</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus>"
			}
			return ""
		})
		_, s.sinks = parseSinkProtocolInfo("http-get:*:video/mp2t:*")
		for _, stream := range []string{track, "http://192.0.2.1:8080/media/second"} {
			u, _ := url.Parse(stream)
			if err := s.Play(t.Context(), device.Playback{URL: u, Play: devicetest.Source(mediav1.Container_CONTAINER_MPEGTS)}); err != nil {
				t.Fatal(err)
			}
		}
		u, _ := url.Parse("http://192.0.2.1:8080/media/second")
		if head, err := s.Position(t.Context()); !errors.Is(err, device.ErrNoPosition) {
			t.Errorf("the first stream's playhead = %v, %v; want no position of the second", head, err)
		}
		track, rel = u.String(), "0:00:00"
		if head, err := s.Position(t.Context()); !errors.Is(err, device.ErrNoPosition) {
			t.Errorf("a 0:00:00 of the second stream = %v, %v; want no position: only the first was seen moving", head, err)
		}
		rel = "0:00:03"
		if head, err := s.Position(t.Context()); err != nil || head.At != 3*time.Second {
			t.Errorf("the second stream moving = %v, %v; want 3s", head, err)
		}
	})
}

func TestALateAnswerAboutAReplacedStreamKeepsTheNewStreamsTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rel := "0:00:42"
		held := make(chan struct{})
		var holding atomic.Bool
		holding.Store(true)
		s := renderer(func(action string) string {
			switch action {
			case "GetPositionInfo":
				return "<RelTime>" + rel + "</RelTime>"
			case "GetTransportInfo":
				if holding.CompareAndSwap(true, false) {
					<-held
				}
				return "<CurrentTransportState>PLAYING</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus>"
			}
			return ""
		})
		_, s.sinks = parseSinkProtocolInfo("http-get:*:video/mp2t:*")
		u, _ := url.Parse("http://192.0.2.1:8080/media/first")
		_ = s.Play(t.Context(), device.Playback{URL: u, Play: devicetest.Source(mediav1.Container_CONTAINER_MPEGTS)})
		late := make(chan struct{})
		go func() { _, _ = s.Position(t.Context()); close(late) }()
		synctest.Wait()
		u, _ = url.Parse("http://192.0.2.1:8080/media/second")
		_ = s.Play(t.Context(), device.Playback{URL: u, Play: devicetest.Source(mediav1.Container_CONTAINER_MPEGTS)})
		if _, err := s.Position(t.Context()); err != nil {
			t.Fatal(err)
		}
		close(held)
		<-late
		rel = "0:00:00"
		if head, err := s.Position(t.Context()); err != nil {
			t.Errorf("the second stream back at 0:00:00 after it moved = %v, %v; the first's late answer erased its tick", head, err)
		}
	})
}

func TestARendererNamingItsTrackOtherwiseStillReportsWhereItPlays(t *testing.T) {
	for _, track := range []string{"", "NOT_IMPLEMENTED", " http://192.0.2.1:8080/media/second ", "HTTP://192.0.2.1:8080/MEDIA/second"} {
		synctest.Test(t, func(t *testing.T) {
			s := renderer(func(action string) string {
				switch action {
				case "GetPositionInfo":
					return "<TrackURI>" + track + "</TrackURI><RelTime>0:00:07</RelTime>"
				case "GetTransportInfo":
					return "<CurrentTransportState>PLAYING</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus>"
				}
				return ""
			})
			_, s.sinks = parseSinkProtocolInfo("http-get:*:video/mp2t:*")
			for _, stream := range []string{"http://192.0.2.1:8080/media/first", "http://192.0.2.1:8080/media/second"} {
				u, _ := url.Parse(stream)
				if err := s.Play(t.Context(), device.Playback{URL: u, Play: devicetest.Source(mediav1.Container_CONTAINER_MPEGTS)}); err != nil {
					t.Fatal(err)
				}
			}
			if head, err := s.Position(t.Context()); err != nil || head.At != 7*time.Second {
				t.Errorf("a renderer naming its track %q = %v, %v; want 7s", track, head, err)
			}
		})
	}
}
