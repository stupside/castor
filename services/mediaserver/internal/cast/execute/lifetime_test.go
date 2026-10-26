package execute

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/testhttp"
)

type captionPlayback struct {
	fakeDevice
	shown []media.Caption
	wait  func(context.Context) error
}

func (d *captionPlayback) Play(_ context.Context, p Playback) error {
	d.shown = p.Captions
	return nil
}

func (d *captionPlayback) AwaitEnd(ctx context.Context) error { return d.wait(ctx) }

func TestHandoffKeepsCaptionsAvailableUntilPlaybackEnds(t *testing.T) {
	caps := chromecastLike(media.MP4)
	caps.Formats[0].Captions = []media.CaptionFormat{media.CaptionVTT}
	s := captionedAttempt(t, caps, []string{"en"})
	listener := &pipeListener{connections: make(chan net.Conn), done: make(chan struct{})}
	s.Publisher = listener
	transport := &http.Transport{DialContext: listener.dial, DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	d := &captionPlayback{caps: caps}
	waited := false
	d.wait = func(ctx context.Context) error {
		waited = true
		if len(d.shown) != 1 {
			return errors.New("caption missing from LOAD")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.shown[0].URL.String(), nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if !strings.Contains(string(body), "hello") {
			return errors.New("caption content missing")
		}
		return nil
	}
	s.attempt.Program = programFromStream(t, handoffStream())
	c := Cast{Machinery: s.Machinery, Device: d, Publisher: s.Publisher, Captions: s.Captions}
	result := c.Run(t.Context(), s.attempt)
	if result.Err != nil || !waited {
		t.Fatalf("playback ended: %v, awaited=%v", result.Err, waited)
	}
	resp, err := client.Get(d.shown[0].URL.String())
	if err == nil {
		resp.Body.Close()
		t.Fatal("caption listener leaked after playback")
	}
}

// pipeListener exercises the real HTTP serving lifecycle without binding a port.
type pipeListener struct {
	connections chan net.Conn
	done        chan struct{}
	once        sync.Once
}

func (l *pipeListener) Publish(ctx context.Context, h http.Handler) (output.Publication, error) {
	return testhttp.Publish(ctx, l, h), nil
}
func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }
func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connections:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.connections <- server:
		return client, nil
	case <-l.done:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	}
}

func TestHandoffPropagatesPlaybackFailureAndCancellation(t *testing.T) {
	failed := errors.New("receiver decoder failed")
	for _, ending := range []error{failed, context.Canceled} {
		d := &captionPlayback{caps: chromecastLike(media.MP4)}
		d.wait = func(context.Context) error { return ending }
		s := newCoordinator(t.Context(), Cast{Device: d}, recovery.Attempt{Program: programFromStream(t, handoffStream())})
		_, err := s.run()
		_ = s.releases.release()
		if !errors.Is(err, ending) {
			t.Fatalf("got %v, want %v", err, ending)
		}
	}
}
