// Package roku is an ECP endpoint with castor's channel already sideloaded, so castor never tries to install it.
package roku

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/receiver"
	"github.com/stupside/castor/e2e/strategy"
)

// plays is what castor's Roku channel decodes.
var plays = receiver.Plays{
	Video: map[string]int{"h264": 8},
	Audio: map[string]int{"aac": 6, "ac3": 0, "eac3": 0},
}

// unreported is how long castor needs to notice a Roku relay ended: ECP never says, so castor waits out its 30s idle rule.
const unreported = 30*time.Second + receiver.Reported

// formats maps the channel's format parameter back to the MIME it stands for.
var formats = map[string]string{"hls": "application/x-mpegURL", "mp4": "video/mp4", "mkv": "video/x-matroska"}

// playerStates is ECP's word for each playback state.
var playerStates = map[receiver.Playback]string{
	receiver.Idle:    "none",
	receiver.Playing: "play",
	receiver.Ended:   "stop",
	receiver.Stopped: "stop",
	receiver.Starved: "stop",
}

// Family builds the one Roku there is: castor's channel decodes what it decodes on every model.
type Family struct{}

func (Family) Name() string { return "roku" }

// Settings are how this Roku misbehaves: RefuseLaunches answers the first n launches 503, as a channel still starting.
type Settings struct {
	RefuseLaunches int `yaml:"refuse_launches"`
}

func (Family) Build(raw yaml.Node) (receiver.Device, error) {
	var settings Settings
	if err := strategy.Decode(raw, &settings); err != nil {
		return nil, fmt.Errorf("roku: %w", err)
	}
	return &device{refuse: settings.RefuseLaunches}, nil
}

type device struct {
	mu     sync.Mutex
	refuse int
}

func (*device) Type() string { return "roku" }

// refused spends one refusal, if any are left.
func (d *device) refused() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse == 0 {
		return false
	}
	d.refuse--
	return true
}

func (d *device) Start(t *testing.T, s *receiver.Session) (receiver.Endpoint, error) {
	s.PlayInRealtime()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /query/apps", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><apps><app id="dev" type="appl" version="1.0.0">Castor</app></apps>`))
	})
	mux.HandleFunc("POST /launch/{app}", func(w http.ResponseWriter, req *http.Request) {
		if d.refused() {
			http.Error(w, "channel not ready", http.StatusServiceUnavailable)
			return
		}
		if app := req.PathValue("app"); app != "dev" {
			s.Problem("launched channel %q, want the sideloaded dev channel", app)
		}
		format := req.URL.Query().Get("format")
		declared, ok := formats[format]
		if !ok {
			s.Problem("launch format %q is none the channel plays", format)
		}
		s.Hand(req.URL.Query().Get("url"), declared)
	})
	mux.HandleFunc("GET /query/media-player", func(w http.ResponseWriter, _ *http.Request) {
		state, position := playerStates[s.State()], ""
		if s.State() == receiver.Playing {
			position = fmt.Sprintf("<position>%d ms</position>", s.Played().Milliseconds())
			if s.Held() {
				state = "pause"
			}
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><player error="false" state="%s">%s</player>`, state, position)
	})
	// ECP has one Play key, which the channel toggles on.
	mux.HandleFunc("POST /keypress/{key}", func(w http.ResponseWriter, req *http.Request) {
		switch key := req.PathValue("key"); key {
		case "Home":
			s.Stop()
			return
		case "Play":
		default:
			s.Problem("keypress %s, want Play", key)
			return
		}
		if s.State() != receiver.Playing {
			s.Problem("keypress Play while not playing")
			return
		}
		s.Hold(!s.Held())
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return receiver.Endpoint{Host: strings.TrimPrefix(server.URL, "http://"), Plays: plays, EndsWithin: unreported}, nil
}
