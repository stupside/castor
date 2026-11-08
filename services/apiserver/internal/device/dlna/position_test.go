package dlna

import (
	"errors"
	"net/url"
	"testing"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"

	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/device/devicetest"
)

func TestParseClock(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"0:05:42.035": 5*time.Minute + 42*time.Second + 35*time.Millisecond,
		"1:22:11":     time.Hour + 22*time.Minute + 11*time.Second,
	} {
		if got, err := parseClock(in); err != nil || got != want {
			t.Errorf("parseClock(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "NOT_IMPLEMENTED", "1:2", "NaN:00:00", "1e3:00:00", "0:61:00", "0:00:01.x"} {
		if _, err := parseClock(bad); err == nil {
			t.Errorf("parseClock(%q) succeeded", bad)
		}
	}
}

// clockRenderer answers GetPositionInfo with rel and GetTransportInfo with state.
func clockRenderer(rel, state *string) *session {
	s := renderer(func(action string) string {
		switch action {
		case "GetPositionInfo":
			return "<RelTime>" + *rel + "</RelTime>"
		case "GetTransportInfo":
			return "<CurrentTransportState>" + *state + "</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus>"
		}
		return ""
	})
	_, s.sinks = parseSinkProtocolInfo("http-get:*:video/mp2t:*")
	return s
}

func TestAStoppedRendererReportsNoPosition(t *testing.T) {
	rel, state := "0:00:42", "PLAYING"
	s := clockRenderer(&rel, &state)
	if head, err := s.Position(t.Context()); err != nil || head.At != 42*time.Second {
		t.Fatalf("Position() while playing = %v, %v; want 42s", head, err)
	}
	for _, stopped := range []struct{ rel, state string }{
		{"0:00:00", "STOPPED"},
		{"0:00:42", "STOPPED"},
		{"0:00:00", "NO_MEDIA_PRESENT"},
	} {
		rel, state = stopped.rel, stopped.state
		if head, err := s.Position(t.Context()); !errors.Is(err, device.ErrNoPosition) {
			t.Errorf("Position() at %s %s = %v, %v; want no position", rel, state, head, err)
		}
	}
	// A renderer still opening the stream is where it says, so a playhead that never moves stays visible.
	rel, state = "0:00:35", "TRANSITIONING"
	if head, err := s.Position(t.Context()); err != nil || head.At != 35*time.Second {
		t.Errorf("Position() while transitioning = %v, %v; want 35s", head, err)
	}
}

func TestAZeroPositionSaysNothingUntilTheNewStreamMoves(t *testing.T) {
	rel, state := "0:00:42", "PLAYING"
	s := clockRenderer(&rel, &state)
	if _, err := s.Position(t.Context()); err != nil {
		t.Fatalf("Position() = %v", err)
	}
	u, _ := url.Parse("http://192.0.2.1:8080/media/stream")
	if err := s.Play(t.Context(), device.Playback{URL: u, Play: devicetest.Source(mediav1.Container_CONTAINER_MPEGTS)}); err != nil {
		t.Fatalf("Play() = %v", err)
	}
	rel, state = "0:00:00", "PLAYING"
	if head, err := s.Position(t.Context()); !errors.Is(err, device.ErrNoPosition) {
		t.Errorf("Position() at 0:00:00 after a new Play = %v, %v; want no position", head, err)
	}
	rel = "0:00:01"
	if head, err := s.Position(t.Context()); err != nil || head.At != time.Second {
		t.Fatalf("Position() once the new stream moved = %v, %v; want 1s", head, err)
	}
	rel = "0:00:00"
	if head, err := s.Position(t.Context()); err != nil || head.At != 0 {
		t.Errorf("Position() back at 0:00:00 after moving = %v, %v; want 0s", head, err)
	}
}
