package roku

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

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
		if err := dev.Play(t.Context(), device.Playback{URL: stream, Play: devicetest.Source(container)}); err != nil {
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
	if err := dev.Play(t.Context(), device.Playback{URL: &url.URL{Scheme: "http", Host: "media.test"}, Play: devicetest.Source(mediav1.Container_CONTAINER_MPEGTS)}); err == nil {
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

// scriptedECP answers each media-player query with the next state, "state@channel" naming the channel holding the player and a leading "!" a failed one, the last one repeating, and records the keys pressed.
type scriptedECP struct {
	states []string
	polls  int
	keys   []string
}

func (e *scriptedECP) RoundTrip(r *http.Request) (*http.Response, error) {
	if key, ok := strings.CutPrefix(r.URL.Path, "/keypress/"); ok {
		e.keys = append(e.keys, key)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	}
	state, channel, held := strings.Cut(e.states[min(e.polls, len(e.states)-1)], "@")
	state, failed := strings.CutPrefix(state, "!")
	e.polls++
	plugin := ""
	if held {
		plugin = `<plugin id="` + channel + `"/>`
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`<player error="` + strconv.FormatBool(failed) + `" state="` + state + `">` + plugin + `</player>`))}, nil
}

func scriptedSession(ecp *scriptedECP) *session {
	return &session{ecp: &url.URL{Scheme: "http", Host: "192.0.2.10:8060"}, appID: "dev", name: "Bedroom Roku", hc: &http.Client{Transport: ecp}}
}

func TestRokuEndsWhenThePlayerClosesAfterPlaying(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ecp := &scriptedECP{states: []string{"none", "startup", "buffer", "play", "pause", "play", "close"}}
		if err := scriptedSession(ecp).AwaitEnd(t.Context()); err != nil {
			t.Fatalf("AwaitEnd = %v, want a clean end", err)
		}
		if ecp.polls != len(ecp.states) {
			t.Errorf("polled %d times, want %d: a player that has not played yet is not over", ecp.polls, len(ecp.states))
		}
	})
}

func TestRokuEndsCleanlyWhenAnotherChannelTakesThePlayer(t *testing.T) {
	for _, tt := range []struct {
		name   string
		states []string
	}{
		{"playing", []string{"play@dev", "play@dev", "play@12"}},
		{"failing", []string{"play@dev", "!stop@12"}},
		{"still held by the previous channel at startup", []string{"play@12", "startup@dev", "play@dev", "close@dev"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ecp := &scriptedECP{states: tt.states}
				if err := scriptedSession(ecp).AwaitEnd(t.Context()); err != nil {
					t.Fatalf("AwaitEnd = %v, want a clean end", err)
				}
				if ecp.polls != len(ecp.states) {
					t.Errorf("polled %d times, want %d: castor's cast is over once another channel holds the player after castor's played", ecp.polls, len(ecp.states))
				}
			})
		})
	}
}

func TestRokuPauseAndResumePressPlayOnlyWhenTheStateCallsForIt(t *testing.T) {
	for _, tt := range []struct {
		name  string
		state string
		do    func(*session) error
		keys  int
		fails bool
	}{
		{"pausing a playing player", "play", func(s *session) error { return s.Pause(context.Background()) }, 1, false},
		{"pausing a paused player", "pause", func(s *session) error { return s.Pause(context.Background()) }, 0, false},
		{"resuming a paused player", "pause", func(s *session) error { return s.Resume(context.Background()) }, 1, false},
		{"resuming a playing player", "play", func(s *session) error { return s.Resume(context.Background()) }, 0, false},
		{"pausing a closed player", "close", func(s *session) error { return s.Pause(context.Background()) }, 0, true},
		{"resuming another channel's paused player", "pause@12", func(s *session) error { return s.Resume(context.Background()) }, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ecp := &scriptedECP{states: []string{tt.state}}
			err := tt.do(scriptedSession(ecp))
			if (err != nil) != tt.fails || len(ecp.keys) != tt.keys {
				t.Errorf("err = %v, keys = %v, want %d key(s) and failure %v", err, ecp.keys, tt.keys, tt.fails)
			}
		})
	}
}

func TestRokuStopLeavesForHome(t *testing.T) {
	ecp := &scriptedECP{states: []string{"play"}}
	if err := scriptedSession(ecp).Stop(t.Context()); err != nil || len(ecp.keys) != 1 || ecp.keys[0] != "Home" {
		t.Errorf("Stop = %v with keys %v, want Home", err, ecp.keys)
	}
}

func TestRokuStartupHasDeadline(t *testing.T) {
	for _, state := range []string{"none", "startup", "buffer"} {
		t.Run(state, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ecp := &scriptedECP{states: []string{state}}
				start := time.Now()
				err := scriptedSession(ecp).AwaitEnd(t.Context())
				if _, ok := errors.AsType[*device.PlaybackError](err); !ok {
					t.Fatalf("AwaitEnd=%v", err)
				}
				if time.Since(start) > startupTimeout+device.PollInterval {
					t.Fatal("startup exceeded deadline")
				}
			})
		})
	}
}

type decoderErrorECP struct{}

func (decoderErrorECP) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`<player error="true" state="stop"><error-code>42</error-code><error-message>unsupported stream</error-message></player>`))}, nil
}
func TestRokuDecoderErrorIsTerminal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dev := &session{ecp: &url.URL{Scheme: "http", Host: "roku.test"}, hc: &http.Client{Transport: decoderErrorECP{}}}
		start := time.Now()
		err := dev.AwaitEnd(t.Context())
		if err == nil || !strings.Contains(err.Error(), "unsupported stream") || !strings.Contains(err.Error(), "42") {
			t.Fatalf("AwaitEnd=%v", err)
		}
		if time.Since(start) != device.PollInterval {
			t.Fatal("decoder refusal retried as network failure")
		}
	})
}

type answeringECP string

func (body answeringECP) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}

func TestRokuPositionIsThePlayersWhileItHoldsTheMedia(t *testing.T) {
	for _, tt := range []struct {
		name, player string
		want         device.Playhead
		err          error
	}{
		{"playing", `<player error="false" state="play"><plugin id="dev"/><position>12345 ms</position><duration>600000 ms</duration></player>`, device.Playhead{At: 12345 * time.Millisecond}, nil},
		{"paused", `<player error="false" state="pause"><position>5000 ms</position></player>`, device.Playhead{At: 5 * time.Second, Paused: true}, nil},
		{"no position", `<player error="false" state="play"/>`, device.Playhead{}, device.ErrNoPosition},
		{"an unreadable position", `<player error="false" state="play"><position>soon</position></player>`, device.Playhead{}, device.ErrNoPosition},
		{"closed", `<player error="false" state="close"><position>5000 ms</position></player>`, device.Playhead{}, device.ErrNoPosition},
		{"failed", `<player error="true" state="stop"><position>5000 ms</position></player>`, device.Playhead{}, device.ErrNoPosition},
		{"another channel's", `<player error="false" state="play"><plugin id="12"/><position>5000 ms</position></player>`, device.Playhead{}, device.ErrNoPosition},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dev := &session{ecp: &url.URL{Scheme: "http", Host: "roku.test"}, appID: "dev", hc: &http.Client{Transport: answeringECP(tt.player)}}
			if got, err := dev.Position(t.Context()); got != tt.want || !errors.Is(err, tt.err) {
				t.Errorf("Position = %v, %v; want %v, %v", got, err, tt.want, tt.err)
			}
		})
	}
}

func TestHLSDoesNotImplyProgressiveWebVTT(t *testing.T) {
	for _, f := range (&session{}).Capabilities().Formats {
		if f.GetHls().GetSubtitles() == mediav1.HLSSupport_SUBTITLES_WEBVTT {
			t.Fatal("unverified subtitle rendition support")
		}
	}
}
