package hls

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/testhttp"
)

// Only a GET that hands over a present media artifact counts; the playlist, a HEAD and a 404 do not.
func TestServedCountsOnlyMediaHandedOver(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"stream.m3u8":       "#EXTM3U\n#EXTINF:4,\nseg_00001.m4s\n",
		"seg_00001.m4s":     "\x00\x00\x00\x18moof",
		"seg_00002.m4s":     "",
		"init.mp4":          "initialization",
		"seg_00003.m4s.tmp": "partial",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, tt := range []struct {
		method, path string
		want         int
		contentType  string
	}{
		{http.MethodGet, "/stream.m3u8", 0, media.HLS},
		{http.MethodHead, "/seg_00001.m4s", 0, ""},
		{http.MethodGet, "/seg_00000.m4s", 0, ""},
		{http.MethodGet, "/", 0, ""},
		{http.MethodGet, "/seg_00002.m4s", 0, ""},
		{http.MethodGet, "/init.mp4", 0, media.MP4},
		{http.MethodGet, "/seg_00003.m4s.tmp", 0, ""},
		{http.MethodGet, "/seg_00001.m4s", 1, "video/iso.segment"},
	} {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv, _ := serving(t, dir, testOpening{Headers: map[string]string{"transferMode.dlna.org": "Streaming", "Content-Type": "text/plain"}})
				resp, err := request(t.Context(), srv, tt.method, tt.path)
				if err != nil {
					t.Fatal(err)
				}
				if got := srv.Served(); got != tt.want {
					t.Errorf("the count moved by %d, want %d", got, tt.want)
				}
				if tt.contentType == "" {
					return
				}
				// The artifact's own type wins over the device's; the device's other headers travel.
				if got := resp.Header.Get("Content-Type"); got != tt.contentType {
					t.Errorf("content-type = %q, want %q", got, tt.contentType)
				}
				if got := resp.Header.Get("transferMode.dlna.org"); got != "Streaming" {
					t.Errorf("transferMode.dlna.org = %q, want the device's header", got)
				}
			})
		})
	}
}

func TestAWebReceiverMayReadThePlaylistAndItsSegments(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"stream.m3u8": "#EXTM3U\n#EXTINF:4,\nseg_00001.m4s\n", "seg_00001.m4s": "\x00\x00\x00\x18moof"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/stream.m3u8", "/seg_00001.m4s"} {
		t.Run(path, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv, _ := serving(t, dir, testOpening{})
				resp, err := request(t.Context(), srv, http.MethodGet, path)
				if err != nil {
					t.Fatal(err)
				}
				if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
					t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
				}
			})
		})
	}
}

// The grace is seeded at open, so a device coming late for the tail segments still has its window.
func TestTheIdleGraceStartsBeforeTheFirstRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const grace = 1500 * time.Millisecond
		srv, ended := serving(t, t.TempDir(), testOpening{IdleGrace: grace})
		ended()
		// One tick short: at exactly grace the two timers tie.
		waiting, cancel := context.WithTimeout(t.Context(), grace-time.Nanosecond)
		defer cancel()
		if err := srv.Wait(waiting); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait = %v inside the grace that follows the producer's exit", err)
		}
	})
}

func TestADevicePollingThePlaylistIsNotIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "stream.m3u8"), []byte("#EXTM3U\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		const poll = 50 * time.Millisecond
		srv, ended := serving(t, dir, testOpening{IdleGrace: 6 * poll})
		ended()

		polling, stop := context.WithCancel(t.Context())
		defer stop()
		go func() {
			for polling.Err() == nil {
				_, _ = request(polling, srv, http.MethodGet, "/stream.m3u8")
				time.Sleep(poll)
			}
		}()

		watching, giveUp := context.WithTimeout(t.Context(), 20*poll)
		defer giveUp()
		if err := srv.Wait(watching); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait = %v while the device was polling the playlist", err)
		}

		stop()
		drained, cancel := context.WithTimeout(t.Context(), 6*poll+time.Second)
		defer cancel()
		if err := srv.Wait(drained); err != nil {
			t.Errorf("Wait = %v once the device stopped asking, want nil", err)
		}
	})
}

func TestAHungSegmentClientIsReleasedByTheWriteDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		f, err := os.Create(filepath.Join(dir, "seg_00000.m4s"))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(32 << 20); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		_ = f.Close()
		const deadline = 100 * time.Millisecond
		srv, _ := serving(t, dir, testOpening{WriteDeadline: deadline})
		u := srv.URL()
		u = u.ResolveReference(&url.URL{Path: "seg_00000.m4s"})
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u.String(), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := device(srv.o).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if _, err := io.ReadFull(resp.Body, make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		// The client takes no more bytes, so only the server's write deadline frees the handler.
		synctest.Wait()
		if srv.Served() != 0 {
			t.Fatal("the segment handler finished before a client that stopped reading took the segment")
		}
		synctest.Sleep(deadline)
		if srv.Served() == 0 {
			t.Fatal("the segment handler remained blocked writing to a client that stopped reading")
		}
	})
}

// serving serves dir's stream.m3u8 on o; end closes the producer, which starts the idle grace.
func serving(t *testing.T, dir string, o testOpening) (srv *testSegments, end func()) {
	t.Helper()
	pr, pw := io.Pipe()
	o.Publisher = newLAN(t)
	o.Format = format()
	o.IdleGrace = cmp.Or(o.IdleGrace, 30*time.Second)
	srv, err := serveSegmentsFixture(t.Context(), o, dir, pr)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	end = sync.OnceFunc(func() { _ = pw.Close() })
	t.Cleanup(func() {
		end()
		_ = srv.Close()
	})
	return srv, end
}

func request(ctx context.Context, srv *testSegments, method, path string) (*http.Response, error) {
	u := srv.URL()
	u = u.ResolveReference(&url.URL{Path: strings.TrimPrefix(path, "/")})
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := device(srv.o).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return resp, err
}

// lan is an in-memory network where these tests' devices reach a delivery, so a bubble's clock rules it.
type lan struct {
	mu     sync.Mutex
	open   map[string]*lanListener
	client *http.Client
}

func newLAN(t *testing.T) *lan {
	l := &lan{open: map[string]*lanListener{}}
	transport := &http.Transport{DialContext: l.dial}
	t.Cleanup(transport.CloseIdleConnections)
	l.client = &http.Client{Transport: transport}
	return l
}

// device is the client of the lan a delivery was opened on.
func device(o testOpening) *http.Client { return o.Publisher.(*lan).client }

func (l *lan) Publish(ctx context.Context, h http.Handler) (output.Publication, error) {
	l.mu.Lock()
	ln := &lanListener{addr: lanAddr(fmt.Sprintf("10.0.0.%d:80", len(l.open)+1)), conns: make(chan net.Conn), closed: make(chan struct{})}
	l.open[ln.addr.String()] = ln
	l.mu.Unlock()
	return testhttp.Publish(ctx, ln, h), nil
}

func (l *lan) dial(ctx context.Context, _, addr string) (net.Conn, error) {
	l.mu.Lock()
	ln := l.open[addr]
	l.mu.Unlock()
	if ln == nil {
		return nil, fmt.Errorf("no delivery at %s", addr)
	}
	near, far := net.Pipe()
	select {
	case ln.conns <- far:
		return near, nil
	case <-ln.closed:
		near.Close()
		far.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		near.Close()
		far.Close()
		return nil, ctx.Err()
	}
}

type lanListener struct {
	addr   lanAddr
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *lanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *lanListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *lanListener) Addr() net.Addr { return l.addr }

type lanAddr string

func (lanAddr) Network() string  { return "lan" }
func (a lanAddr) String() string { return string(a) }

func TestProducerCancellationKeepsPublishedSegmentsAvailable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seg_00001.m4s"), []byte("retained segment"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader, writer := io.Pipe()
	defer writer.Close()
	srv, err := serveSegmentsFixture(ctx, testOpening{Publisher: newLAN(t), Format: format()}, dir, reader)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	cancel()
	<-srv.Produced()
	resp, err := request(t.Context(), srv, http.MethodGet, "/seg_00001.m4s")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retained segment returned %d after producer cancellation", resp.StatusCode)
	}
}

// traceEntry is one record a traceSink was handed.
type traceEntry struct {
	message string
	level   slog.Level
	attrs   map[string]any
}

// traceSink keeps every record it is handed, so a test can read what was logged.
type traceSink struct {
	mu      sync.Mutex
	entries []traceEntry
}

func (s *traceSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *traceSink) Handle(_ context.Context, record slog.Record) error {
	attrs := map[string]any{}
	record.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, traceEntry{message: record.Message, level: record.Level, attrs: attrs})
	return nil
}

func (s *traceSink) WithAttrs([]slog.Attr) slog.Handler { return s }

func (s *traceSink) WithGroup(string) slog.Handler { return s }

// Every request a receiver makes is traced at debug level, a 404 included.
func TestEveryReceiverRequestIsTracedAtDebugLevel(t *testing.T) {
	const playlist = "#EXTM3U\n#EXTINF:4,\nseg_00001.m4s\n"
	const segment = "\x00\x00\x00\x18moof"
	dir := t.TempDir()
	for name, content := range map[string]string{"stream.m3u8": playlist, "seg_00001.m4s": segment} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sink := &traceSink{}
	previous := slog.Default()
	slog.SetDefault(slog.New(sink))
	t.Cleanup(func() { slog.SetDefault(previous) })

	synctest.Test(t, func(t *testing.T) {
		srv, _ := serving(t, dir, testOpening{})
		for _, path := range []string{"/stream.m3u8", "/seg_00001.m4s", "/seg_00009.m4s"} {
			if _, err := request(t.Context(), srv, http.MethodGet, path); err != nil {
				t.Fatal(err)
			}
		}
		// The handler logs after its answer is written, so let it finish before reading the sink.
		synctest.Wait()
	})

	sink.mu.Lock()
	defer sink.mu.Unlock()
	var traced []traceEntry
	for _, entry := range sink.entries {
		if entry.message == "receiver asked the HLS output" {
			traced = append(traced, entry)
		}
	}
	if len(traced) != 3 {
		t.Fatalf("traced %d receiver requests, want 3", len(traced))
	}
	wants := []struct {
		path   string
		status int64
	}{
		{"/stream.m3u8", 200},
		{"/seg_00001.m4s", 200},
		{"/seg_00009.m4s", 404},
	}
	for i, want := range wants {
		entry := traced[i]
		if entry.level != slog.LevelDebug {
			t.Errorf("request %d logged at %v, want %v", i, entry.level, slog.LevelDebug)
		}
		if got := entry.attrs["path"]; got != want.path {
			t.Errorf("request %d path = %v, want %s", i, got, want.path)
		}
		if got := entry.attrs["status"]; got != want.status {
			t.Errorf("request %d status = %v, want %d", i, got, want.status)
		}
	}
	if got := traced[0].attrs["bytes"]; got != int64(len(playlist)) {
		t.Errorf("playlist bytes = %v, want %d", got, len(playlist))
	}
	if got := traced[1].attrs["bytes"]; got != int64(len(segment)) {
		t.Errorf("segment bytes = %v, want %d", got, len(segment))
	}
}

// A rolling playlist is handed over once a live player can start behind its edge; a retained event at its first fragment.
func TestARollingPlaylistLandsOnceALivePlayerCanStartBehindItsEdge(t *testing.T) {
	playlist := func(fragments int, ended bool) string {
		body := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-MAP:URI=\"init.mp4\"\n"
		for i := range fragments {
			body += fmt.Sprintf("#EXTINF:4.000000,\nseg_%05d.m4s\n", i)
		}
		if ended {
			body += "#EXT-X-ENDLIST\n"
		}
		return body
	}
	for _, tt := range []struct {
		name      string
		runtime   time.Duration
		fragments int
		ended     bool
		lands     bool
	}{
		{name: "a rolling playlist with no file yet has not landed", fragments: -1},
		{name: "a rolling playlist of one fragment is held back", fragments: 1},
		{name: "a rolling playlist of two fragments is held back", fragments: 2},
		{name: "a rolling playlist of three fragments lands", fragments: 3, lands: true},
		{name: "a rolling playlist that ended short is whole", fragments: 1, ended: true, lands: true},
		{name: "a retained event lands at its first fragment", runtime: time.Minute, fragments: 1, lands: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			o := output.Opening{Format: format()}
			s, err := newSegments(o, dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			s.runtime = tt.runtime
			if tt.fragments >= 0 {
				if err := os.WriteFile(filepath.Join(dir, o.Format.Tuning.Output), []byte(playlist(tt.fragments, tt.ended)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			size, err := s.Artifact().Landed()
			if err != nil {
				t.Fatal(err)
			}
			if landed := size > 0; landed != tt.lands {
				t.Fatalf("landed = %v (size %d), want %v", landed, size, tt.lands)
			}
		})
	}
}
