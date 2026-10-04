package roku

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/device/devicetest"
)

func TestTheChannelIsLaunchedWithTheFormatOfWhatItPlays(t *testing.T) {
	launched := make(chan url.Values, 1)
	ts := httptest.NewTestServer(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/launch/dev" {
			launched <- r.URL.Query()
		}
	}))
	// The server has its URL once its client is made.
	hc := ts.Client()
	ecp, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	dev := &session{ecp: ecp, appID: "dev", name: "Bedroom Roku", hc: hc}
	stream := &url.URL{Scheme: "http", Host: "192.0.2.1:8080", Path: "/stream"}
	for container, want := range map[mediav1.Container]string{
		mediav1.Container_CONTAINER_MP4: "mp4",
		mediav1.Container_CONTAINER_MKV: "mkv",
		mediav1.Container_CONTAINER_HLS: "hls",
	} {
		if err := dev.Play(t.Context(), stream, container); err != nil {
			t.Fatal(err)
		}
		if got := <-launched; got.Get("format") != want || got.Get("url") != stream.String() {
			t.Errorf("playing %v launched the channel with %v, want format %q of the stream", container, got, want)
		}
	}
}

func TestUnsupportedContainersDoNotLaunchTheRokuChannel(t *testing.T) {
	// No client is needed: an unsupported container must fail before ECP.
	dev := &session{}
	if err := dev.Play(t.Context(), &url.URL{Scheme: "http", Host: "media.test"}, mediav1.Container_CONTAINER_MPEGTS); err == nil {
		t.Error("Play accepted unsupported MPEG-TS container")
	}
}

// refusingAfter answers the media-player query as playing its first polls, then refuses every one, as a Roku unplugged would.
type refusingAfter struct{ answers, polls atomic.Int32 }

func (e *refusingAfter) RoundTrip(*http.Request) (*http.Response, error) {
	if e.polls.Add(1) > e.answers.Load() {
		return nil, errors.New("connect: connection refused")
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`<player error="false" state="play"/>`))}, nil
}

func TestARokuThatStopsAnsweringIsNamedGone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ecp := &refusingAfter{}
		ecp.answers.Store(2)
		dev := &session{ecp: &url.URL{Scheme: "http", Host: "192.0.2.10:8060"}, name: "Bedroom Roku", hc: &http.Client{Transport: ecp}}
		away, gone := errors.AsType[*device.Gone](dev.AwaitEnd(t.Context()))
		if !gone {
			t.Fatal("AwaitEnd did not name the Roku gone once it stopped answering")
		}
		if away.Device != "Bedroom Roku" || away.Err == nil {
			t.Errorf("gone = %+v, want the device named and the last failure carried", away)
		}
	})
}

// playingECP answers every media-player query as playing, counting the polls.
type playingECP struct{ polls atomic.Int32 }

func (e *playingECP) RoundTrip(*http.Request) (*http.Response, error) {
	e.polls.Add(1)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`<player error="false" state="play"/>`))}, nil
}

func TestRokuAnswersWhenTheCastEnds(t *testing.T) {
	ecp := &playingECP{}
	devicetest.AwaitsTheCastsEnd(t, func() device.Device {
		return &session{ecp: &url.URL{Scheme: "http", Host: "192.0.2.10:8060"}, name: "Bedroom Roku", hc: &http.Client{Transport: ecp}}
	})
	if ecp.polls.Load() == 0 {
		t.Error("the suite never polled the Roku")
	}
}
