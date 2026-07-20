// Package replay serves a single producer's stream over local HTTP, replaying
// it to every client from byte 0.
//
// The producer's output is spooled to disk and every HTTP connection replays
// it from the start through its own reader. This is what makes Samsung's
// HEAD-probe → short-GET → real-GET dance safe: the probe GET reads its own
// copy of the stream head and the real GET still starts at byte 0 with the
// container init and first keyframe intact. A live fan-out broadcaster (the
// previous design) hands the probe the only copy of the stream head, and the
// real GET then joins mid-stream at an arbitrary byte offset the renderer
// cannot decode.
//
// Delivery is not rate-limited: each connection is written as fast as the
// renderer reads it, and TCP backpressure holds it to the renderer's own
// playback rate. The producer runs ahead into the spool as fast as it
// encodes, so a late or reconnecting client can always be served from the
// start.
package replay

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/stupside/castor/internal/cast/spool"
)

const (
	// sendChunkSize is the per-connection read/write granularity.
	sendChunkSize = 32 * 1024

	// idleGrace is how long Wait keeps the server alive after the stream is
	// fully produced and the last client dropped mid-stream — long enough
	// for a renderer hiccup/reconnect, short enough not to hang the CLI.
	idleGrace = 30 * time.Second
)

// Config is what the planner fills in.
type Config struct {
	LocalIP     string
	ContentType string
	Extension   string
	Headers     map[string]string

	// SpoolPath is where the producer's output is spooled. The caller owns
	// the file's directory lifecycle.
	SpoolPath string
}

// Server spools a producer's output and replays it to every HTTP client from
// the beginning.
type Server struct {
	cfg Config

	listener net.Listener
	server   *http.Server
	cancel   context.CancelFunc
	spool    *spool.Spool

	done chan struct{} // producer fully spooled

	mu             sync.Mutex
	active         int
	completed      bool // some client consumed the stream to EOF
	lastDisconnect time.Time
}

// New binds to cfg.LocalIP on an ephemeral port and starts spooling producer
// in the background.
func New(cfg Config, producer io.Reader) (*Server, error) {
	sp, err := spool.New(cfg.SpoolPath)
	if err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp", cfg.LocalIP+":0")
	if err != nil {
		sp.CloseWrite(nil)
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:            cfg,
		listener:       ln,
		cancel:         cancel,
		spool:          sp,
		done:           make(chan struct{}),
		lastDisconnect: time.Now(),
	}

	go func() {
		defer close(s.done)
		_, copyErr := io.Copy(sp, producer)
		sp.CloseWrite(copyErr)
		slog.Debug("stream fully spooled", "bytes", sp.Size(), "error", copyErr)
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/stream"+cfg.Extension, func(w http.ResponseWriter, r *http.Request) {
		s.handleStream(ctx, w, r)
	})
	s.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	go func() { _ = s.server.Serve(ln) }()
	return s, nil
}

// URL is the address the renderer should fetch.
func (s *Server) URL() *url.URL {
	return &url.URL{Scheme: "http", Host: s.listener.Addr().String(), Path: "/stream" + s.cfg.Extension}
}

// Close stops accepting connections and severs active ones.
func (s *Server) Close() error {
	s.cancel()
	return s.server.Close()
}

// Wait blocks until the stream has been fully produced AND delivered: a
// client has read it to EOF (movie over), or no client is left and none
// returned within the grace window, or ctx is cancelled. The producer
// finishing is explicitly NOT enough — it runs faster than playback, so the
// renderer is still mid-movie when the spool completes.
func (s *Server) Wait(ctx context.Context) error {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}

		select {
		case <-s.done:
		default:
			continue // still producing
		}

		s.mu.Lock()
		finished := s.active == 0 &&
			(s.completed || time.Since(s.lastDisconnect) > idleGrace)
		s.mu.Unlock()
		if finished {
			return nil
		}
	}
}

// handleStream replays the spool from byte 0 to one client. srvCtx ends the
// stream on server shutdown; the request context ends it on client disconnect.
func (s *Server) handleStream(srvCtx context.Context, w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(srvCtx, cancel)
	defer stop()

	w.Header().Set("Content-Type", s.cfg.ContentType)
	for k, v := range s.cfg.Headers {
		w.Header().Set(k, v)
	}
	if r.Method == http.MethodHead {
		slog.InfoContext(ctx, "stream HEAD", "from", r.RemoteAddr, "user_agent", r.UserAgent())
		w.WriteHeader(http.StatusOK)
		return
	}

	slog.InfoContext(ctx, "stream GET",
		"from", r.RemoteAddr,
		"user_agent", r.UserAgent(),
		"range", r.Header.Get("Range"),
	)

	tail, err := s.spool.Tail(ctx)
	if err != nil {
		http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
		return
	}
	defer tail.Close()

	s.mu.Lock()
	s.active++
	s.mu.Unlock()
	reachedEOF := false
	defer func() {
		s.mu.Lock()
		s.active--
		s.completed = s.completed || reachedEOF
		s.lastDisconnect = time.Now()
		s.mu.Unlock()
	}()

	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	buf := make([]byte, sendChunkSize)
	var sent int64
	for {
		n, readErr := tail.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				slog.InfoContext(ctx, "stream client disconnected", "from", r.RemoteAddr, "bytes_sent", sent, "error", err)
				return
			}
			sent += int64(n)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				reachedEOF = true
				slog.InfoContext(ctx, "stream fully delivered", "from", r.RemoteAddr, "bytes_sent", sent)
			} else {
				slog.InfoContext(ctx, "stream read ended", "from", r.RemoteAddr, "bytes_sent", sent, "error", readErr)
			}
			return
		}
	}
}
