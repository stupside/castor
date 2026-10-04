package receiver

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stupside/castor/e2e/strategy"
)

// Playback is where a session is, in the terms every device protocol reports.
type Playback int

const (
	Idle Playback = iota
	Playing
	// Ended is the media playing out; Stopped is the viewer ending it first.
	Ended
	Stopped
)

// Over reports whether playback has ended either way.
func (p Playback) Over() bool { return p == Ended || p == Stopped }

// ResponseCheck holds the response to the receiver's fetch to a protocol's demands; it returns violations.
type ResponseCheck func(declared string, h http.Header) []string

// Setup is what a session plays with, bound at the composition root.
type Setup struct {
	Tools   Tools
	Players strategy.Registry[Player]
	Viewer  Viewer
}

// Received is one hand-off and what playing it produced.
type Received struct {
	URL string
	// ContentType is what castor declared the handed media to be.
	ContentType string
	Played      Measured
	// Playback is how it ended; Ended is when.
	Playback Playback
	Ended    time.Time
	// Problems are protocol violations the receiver saw.
	Problems []string
}

// Session takes one hand-off and plays it; families drive it from their protocol handlers.
type Session struct {
	setup  Setup
	dir    string
	checks []ResponseCheck
	// headers are what the device puts on its taped fetch, beyond the user agent.
	headers map[string]string

	// ctx and wg bound every goroutine the session runs; ctx ends before the test's cleanup joins them.
	ctx context.Context
	wg  sync.WaitGroup

	mu       sync.Mutex
	handed   int
	received Received
	// first closes on the first hand-off, done when its playback ends.
	first, done chan struct{}
}

func NewSession(t *testing.T, setup Setup) *Session {
	// The tape lands in the artifact directory, so `go test -artifacts` keeps what the receiver played.
	s := &Session{setup: setup, dir: t.ArtifactDir(), ctx: t.Context(), first: make(chan struct{}), done: make(chan struct{})}
	t.Cleanup(s.wg.Wait)
	return s
}

// Context ends when the test does.
func (s *Session) Context() context.Context { return s.ctx }

// Go runs f for as long as the session lives; the test does not end before f returns.
func (s *Session) Go(f func()) { s.wg.Go(f) }

// Done closes when the handed playback ends.
func (s *Session) Done() <-chan struct{} { return s.done }

// Header puts a request header on the taped fetch, as a device's HTTP stack does.
func (s *Session) Header(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.headers == nil {
		s.headers = map[string]string{}
	}
	s.headers[key] = value
}

// Check adds a protocol's demands on the response to the receiver's fetch.
func (s *Session) Check(c ResponseCheck) { s.checks = append(s.checks, c) }

// Problem records a violation for the case to report, safe from any handler goroutine.
func (s *Session) Problem(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received.Problems = append(s.received.Problems, fmt.Sprintf(format, args...))
}

// Hand starts playing the first URL; any later hand-off is a violation, since a case casts exactly once.
func (s *Session) Hand(url, declared string) {
	s.mu.Lock()
	s.handed++
	first := s.handed == 1
	if first {
		s.received.URL, s.received.ContentType = url, declared
	}
	s.mu.Unlock()
	if !first {
		s.Problem("handed a second URL %s; a cast hands over once", url)
		return
	}
	close(s.first)
	s.Go(func() {
		ended := s.play(url)
		s.mu.Lock()
		s.received.Playback, s.received.Ended = ended, time.Now()
		s.mu.Unlock()
		close(s.done)
	})
}

// State is where playback is now.
func (s *Session) State() Playback {
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.received.Playback
	default:
	}
	select {
	case <-s.first:
		return Playing
	default:
		return Idle
	}
}

// Received waits up to handOff for a hand-off, then for its playback to end; false when nothing was handed over.
// A sender that detaches may exit before its last frame lands, so the wait for the hand-off outlives the sender.
func (s *Session) Received(t *testing.T, handOff time.Duration) (Received, bool) {
	t.Helper()
	select {
	case <-s.first:
	case <-time.After(handOff):
		s.mu.Lock()
		defer s.mu.Unlock()
		return Received{Problems: slices.Clone(s.received.Problems)}, false
	}
	select {
	case <-s.done:
	case <-time.After(3 * time.Minute):
		t.Fatal("the receiver was still playing after 3 minutes")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	got := s.received
	got.Problems = slices.Clone(got.Problems)
	return got, true
}

// play fetches url, hands the response to the first player that takes it, and measures the tape.
func (s *Session) play(url string) Playback {
	ctx, cancel := s.setup.Viewer.Watch(s.ctx)
	defer cancel()
	ended := func() Playback {
		if s.setup.Viewer.Stopped(ctx) {
			return Stopped
		}
		return Ended
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		s.Problem("building the fetch: %v", err)
		return Ended
	}
	req.Header.Set("User-Agent", UserAgent)
	s.mu.Lock()
	for key, value := range s.headers {
		req.Header.Set(key, value)
	}
	s.mu.Unlock()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.Problem("fetching %s: %v", url, err)
		return Ended
	}
	defer resp.Body.Close()
	s.mu.Lock()
	declared := s.received.ContentType
	s.mu.Unlock()
	for _, check := range s.checks {
		for _, v := range check(declared, resp.Header) {
			s.Problem("%s", v)
		}
	}
	if resp.StatusCode != http.StatusOK {
		s.Problem("fetching %s: %s", url, resp.Status)
		return Ended
	}

	body := bufio.NewReader(resp.Body)
	head, _ := body.Peek(512)
	player, ok := s.setup.Players.First(func(p Player) bool { return p.Plays(head) })
	if !ok {
		s.Problem("no player takes what %s serves", url)
		return Ended
	}
	tape, err := player.Record(ctx, Recording{
		URL: url, Body: body, Tape: filepath.Join(s.dir, "tape"), FFmpeg: s.setup.Tools.FFmpeg, Viewer: s.setup.Viewer,
	})
	outcome := ended()
	if err != nil && outcome != Stopped {
		s.Problem("%s player on %s: %v", player.Name(), url, err)
		return outcome
	}
	played, err := measure(s.setup.Tools.FFmpeg, s.setup.Tools.FFprobe, tape)
	if err != nil {
		s.Problem("measuring what was played: %v", err)
		return outcome
	}
	s.mu.Lock()
	s.received.Played = played
	s.mu.Unlock()
	return outcome
}
