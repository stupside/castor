package deliver

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Only a GET that hands over a present media artifact counts; the playlist, a HEAD and a 404 do not.
func TestServedCountsOnlyMediaHandedOver(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"stream.m3u8":   "#EXTM3U\n#EXTINF:4,\nseg_00001.m4s\n",
		"seg_00001.m4s": "\x00\x00\x00\x18moof",
		"seg_00002.m4s": "",
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
		{http.MethodGet, "/seg_00001.m4s", 1, "video/iso.segment"},
	} {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv, _ := serving(t, dir, Opening{Headers: map[string]string{"transferMode.dlna.org": "Streaming", "Content-Type": "text/plain"}})
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

// The grace is seeded at open, so a device coming late for the tail segments still has its window.
func TestTheIdleGraceStartsBeforeTheFirstRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const grace = 3 * settleInterval
		srv, ended := serving(t, t.TempDir(), Opening{IdleGrace: grace})
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
		srv, ended := serving(t, dir, Opening{IdleGrace: 6 * poll})
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
		drained, cancel := context.WithTimeout(t.Context(), 6*poll+2*settleInterval)
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
		srv, _ := serving(t, dir, Opening{WriteDeadline: deadline})
		u := srv.URL()
		u.Path = "/seg_00000.m4s"
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
func serving(t *testing.T, dir string, o Opening) (srv *Segments, end func()) {
	t.Helper()
	pr, pw := io.Pipe()
	o.Listeners = newLAN(t)
	o.Format.Tuning.Output = "stream.m3u8"
	o.IdleGrace = cmp.Or(o.IdleGrace, 30*time.Second)
	srv, err := OpenSegments(t.Context(), o, dir, pr)
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

func request(ctx context.Context, srv *Segments, method, path string) (*http.Response, error) {
	u := srv.URL()
	u.Path = path
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
func device(o Opening) *http.Client { return o.Listeners.(*lan).client }

func (l *lan) Listen(context.Context) (net.Listener, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ln := &lanListener{addr: lanAddr(fmt.Sprintf("10.0.0.%d:80", len(l.open)+1)), conns: make(chan net.Conn), closed: make(chan struct{})}
	l.open[ln.addr.String()] = ln
	return ln, nil
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
		return nil, net.ErrClosed
	case <-ctx.Done():
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
