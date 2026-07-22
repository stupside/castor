package device

import (
	"net"
	"testing"

	castmedia "github.com/vishen/go-chromecast/cast"
	pb "github.com/vishen/go-chromecast/cast/proto"
	castdns "github.com/vishen/go-chromecast/dns"
)

func TestPlaybackOver(t *testing.T) {
	status := func(playerState, idleReason string) castmedia.MediaStatusResponse {
		return castmedia.MediaStatusResponse{
			PayloadHeader: castmedia.PayloadHeader{Type: "MEDIA_STATUS"},
			Status:        []castmedia.Media{{PlayerState: playerState, IdleReason: idleReason}},
		}
	}

	tests := []struct {
		name     string
		messages []castmedia.MediaStatusResponse
		done     bool
	}{
		{
			// The receiver reports the fate of whatever it played before this
			// cast; a stale FINISHED must not end a session that hasn't started.
			name:     "stale idle status before playback is ignored",
			messages: []castmedia.MediaStatusResponse{status("IDLE", "FINISHED")},
			done:     false,
		},
		{
			name: "finished after playing ends the cast",
			messages: []castmedia.MediaStatusResponse{
				status("BUFFERING", ""), status("PLAYING", ""), status("IDLE", "FINISHED"),
			},
			done: true,
		},
		{
			name: "user stop on the device ends the cast",
			messages: []castmedia.MediaStatusResponse{
				status("PLAYING", ""), status("IDLE", "CANCELLED"),
			},
			done: true,
		},
		{
			name: "another sender interrupting ends the cast",
			messages: []castmedia.MediaStatusResponse{
				status("PLAYING", ""), status("IDLE", "INTERRUPTED"),
			},
			done: true,
		},
		{
			name: "pause keeps the cast alive",
			messages: []castmedia.MediaStatusResponse{
				status("PLAYING", ""), status("PAUSED", ""),
			},
			done: false,
		},
		{
			// A load transition parks the player IDLE with no reason; only a
			// terminal reason ends the cast.
			name: "idle without a terminal reason keeps the cast alive",
			messages: []castmedia.MediaStatusResponse{
				status("PLAYING", ""), status("IDLE", ""),
			},
			done: false,
		},
		{
			name: "receiver app closing ends the cast",
			messages: []castmedia.MediaStatusResponse{
				{PayloadHeader: castmedia.PayloadHeader{Type: "CLOSE"}},
			},
			done: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var watch mediaWatch
			done := false
			for _, msg := range tt.messages {
				done = playbackOver(&watch, &msg)
			}
			if done != tt.done {
				t.Errorf("playbackOver() = %v, want %v", done, tt.done)
			}
		})
	}
}

func TestWatchMessage(t *testing.T) {
	message := func(payload string) *pb.CastMessage {
		return &pb.CastMessage{PayloadUtf8: &payload}
	}
	closed := func(d *chromecastDevice) bool {
		select {
		case <-d.done:
			return true
		default:
			return false
		}
	}

	dev := &chromecastDevice{done: make(chan struct{})}
	stale := `{"type":"MEDIA_STATUS","status":[{"playerState":"IDLE","idleReason":"FINISHED"}]}`

	// Disarmed (before Play), even a terminal status is ignored.
	dev.watchMessage(message(stale))
	if closed(dev) {
		t.Fatal("terminal status before Play must not end the cast")
	}

	dev.armed.Store(true)

	// Armed but never seen active: a stale terminal status is still ignored.
	dev.watchMessage(message(stale))
	if closed(dev) {
		t.Fatal("terminal status before playback engages must not end the cast")
	}

	dev.watchMessage(message(`{"type":"MEDIA_STATUS","status":[{"playerState":"PLAYING"}]}`))
	dev.watchMessage(message(`not json`)) // receiver noise is skipped, not fatal
	if closed(dev) {
		t.Fatal("playback in progress must not end the cast")
	}

	dev.watchMessage(message(`{"type":"MEDIA_STATUS","status":[{"playerState":"IDLE","idleReason":"CANCELLED"}]}`))
	if !closed(dev) {
		t.Fatal("cancel after playback must end the cast")
	}

	// A second terminal message after done is closed must not re-close (panic).
	dev.watchMessage(message(stale))
}

func TestChromecastInfo(t *testing.T) {
	tests := []struct {
		name  string
		entry castdns.CastEntry
		want  Info
		ok    bool
	}{
		{
			name:  "ipv4 with friendly name on default port",
			entry: castdns.CastEntry{DeviceName: "Office Display", AddrV4: net.ParseIP("192.0.2.10"), Port: chromecastPort},
			want:  Info{Name: "Office Display", Type: TypeChromecast, Address: "192.0.2.10"},
			ok:    true,
		},
		{
			name:  "cast group on non-default port keeps host:port",
			entry: castdns.CastEntry{DeviceName: "Speakers", AddrV4: net.ParseIP("192.0.2.20"), Port: 32541},
			want:  Info{Name: "Speakers", Type: TypeChromecast, Address: "192.0.2.20:32541"},
			ok:    true,
		},
		{
			name:  "ipv6 used when no ipv4 is advertised",
			entry: castdns.CastEntry{DeviceName: "Living Room", AddrV6: net.ParseIP("2001:db8::1"), Port: chromecastPort},
			want:  Info{Name: "Living Room", Type: TypeChromecast, Address: "2001:db8::1"},
			ok:    true,
		},
		{
			name:  "name falls back to the mDNS instance name",
			entry: castdns.CastEntry{Name: "Kitchen", AddrV4: net.ParseIP("192.0.2.30"), Port: chromecastPort},
			want:  Info{Name: "Kitchen", Type: TypeChromecast, Address: "192.0.2.30"},
			ok:    true,
		},
		{
			name:  "entry without an address is rejected",
			entry: castdns.CastEntry{DeviceName: "Office Display"},
			ok:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := chromecastInfo(tt.entry)
			if ok != tt.ok {
				t.Fatalf("chromecastInfo() ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("chromecastInfo() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
