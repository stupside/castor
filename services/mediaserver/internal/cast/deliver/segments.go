package deliver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/container"
)

// Segments serves dir over HTTP, tracks liveness for Wait, deletes behind live edge.
type Segments struct {
	o        Opening
	listener net.Listener
	server   *http.Server
	root     *os.Root
	playlist string

	drained chan struct{}
	served  atomic.Int64 // Artifacts handed over (measure of device fetch, not bytes).
	reader  sync.WaitGroup

	mu          sync.Mutex
	lastRequest time.Time // When device last requested anything (seeded at New).
}

// OpenSegments serves the live HLS directory dir producer writes; no byte pacing, the client self-paces.
func OpenSegments(ctx context.Context, o Opening, dir string, producer io.Reader) (*Segments, error) {
	ln, err := o.Listeners.Listen(ctx)
	if err != nil {
		return nil, fmt.Errorf("starting HLS server: %w", err)
	}
	// A root, so a symlink in the work dir never serves a file outside it.
	root, err := os.OpenRoot(dir)
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("opening the HLS directory: %w", err)
	}

	s := &Segments{
		o:           o,
		listener:    ln,
		root:        root,
		playlist:    filepath.Join(dir, o.Format.Tuning.Output),
		drained:     make(chan struct{}),
		lastRequest: time.Now(),
	}
	s.reader.Go(func() {
		// Drain to EOF (unread pipe stops ffmpeg once kernel buffer fills).
		defer close(s.drained)
		_, _ = io.Copy(io.Discard, producer)
	})

	files := http.FileServerFS(root.FS())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		s.touch()
		if s.o.WriteDeadline > 0 {
			control := http.NewResponseController(w)
			if err := control.SetWriteDeadline(time.Now().Add(s.o.WriteDeadline)); err != nil {
				slog.WarnContext(r.Context(), "HLS write deadline unavailable", "error", err)
			}
			defer control.SetWriteDeadline(time.Time{})
		}
		slog.InfoContext(r.Context(), "hls request", "from", r.RemoteAddr, "path", r.URL.Path)
		// Set device headers first, then artifact's own type.
		for k, v := range s.o.Headers {
			w.Header().Set(k, v)
		}
		// Go doesn't register .m3u8/.m4s; set type before ServeContent sniffs.
		if ct, ok := container.HLSArtifactContentType(r.URL.Path); ok {
			w.Header().Set("Content-Type", ct)
		}
		// Count from answer, not request (404 on window-rolled segments shouldn't count).
		answer := &answered{ResponseWriter: w}
		files.ServeHTTP(answer, r)
		if r.Method == http.MethodGet && answer.carriedBytes() && s.carriesMedia(r.URL.Path) {
			s.handedOver()
		}
	})
	s.server = serve(ctx, ln, mux)
	return s, nil
}

// URL is the media-playlist address the device should play.
func (s *Segments) URL() *url.URL {
	return &url.URL{Scheme: "http", Host: s.listener.Addr().String(), Path: "/" + s.o.Format.Tuning.Output}
}

// Served returns artifacts handed over (zero = URL accepted but no bytes fetched).
func (s *Segments) Served() int { return int(s.served.Load()) }

// Drained is closed once encoder output reaches EOF.
func (s *Segments) Drained() <-chan struct{} { return s.drained }

func (s *Segments) Artifact() Artifact {
	// No patience (zero-byte m3u8 is unparseable).
	return Artifact{Subject: "the HLS playlist", Landed: written(s.playlist)}
}

// touch restarts idle grace on any request (playlist refresh counts as watching).
func (s *Segments) touch() {
	s.mu.Lock()
	s.lastRequest = time.Now()
	s.mu.Unlock()
}

func (s *Segments) handedOver() { s.served.Add(1) }

// carriesMedia returns true for program (anything but playlist) to avoid silent count breakage.
func (s *Segments) carriesMedia(p string) bool {
	name := strings.TrimPrefix(path.Clean("/"+p), "/")
	return name != "" && name != s.o.Format.Tuning.Output
}

// answered tracks response status for Served count (ResponseWriter doesn't report it).
type answered struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (a *answered) WriteHeader(code int) {
	if a.status == 0 {
		// First status is the one sent; second WriteHeader never reaches wire.
		a.status = code
	}
	a.ResponseWriter.WriteHeader(code)
}

func (a *answered) Write(b []byte) (int, error) {
	if a.status == 0 {
		// net/http's implicit 200
		a.status = http.StatusOK
	}
	n, err := a.ResponseWriter.Write(b)
	a.bytes += int64(n)
	return n, err
}

// ReadFrom keeps file server's ReaderFrom (sendfile, not userspace copy).
func (a *answered) ReadFrom(r io.Reader) (int64, error) {
	if a.status == 0 {
		a.status = http.StatusOK
	}
	n, err := io.Copy(a.ResponseWriter, r)
	a.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the connection's own flush and deadlines.
func (a *answered) Unwrap() http.ResponseWriter { return a.ResponseWriter }

// carriedBytes reports successful response (2xx); 404 and 416 hand over nothing.
func (a *answered) carriedBytes() bool { return a.status >= 200 && a.status < 300 && a.bytes > 0 }

// Wait blocks until the encoder's output ended and the device stopped asking, or ctx ends.
func (s *Segments) Wait(ctx context.Context) error {
	return settle(ctx, s.drained, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return time.Since(s.lastRequest) > s.o.IdleGrace
	})
}

// Close stops HTTP server and joins encoder-output-draining goroutine.
func (s *Segments) Close() error {
	err := stop(s.server)
	s.reader.Wait()
	return errors.Join(err, s.root.Close())
}

// written reports file size on disk (zero-byte m3u8 is unparseable).
func written(path string) func() int64 {
	return func() int64 {
		info, err := os.Stat(path)
		if err != nil {
			return 0
		}
		return info.Size()
	}
}
