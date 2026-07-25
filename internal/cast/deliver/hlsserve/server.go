// Package hlsserve serves a live HLS directory (playlist + rolling fMP4 segments)
// over local HTTP. There is no byte pacing: an HLS client self-paces, and ffmpeg
// bounds the on-disk window. URL/Wait/Close match the replay server's shape so
// the cast path composes over either.
package hlsserve

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

// idleGrace is how long to keep serving after the producer finished and the
// client went quiet, so the tail segments still get fetched.
const idleGrace = 30 * time.Second

// Config is what the caller fills in.
type Config struct {
	LocalIP string // address to bind the HTTP listener
	Dir     string // directory ffmpeg writes the playlist and segments into
	// Playlist is the media playlist filename within Dir (media.HLSPlaylistName).
	Playlist string
}

// Server serves Config.Dir over HTTP and tracks liveness so Wait can end the
// cast once the stream is fully produced and drained.
type Server struct {
	cfg      Config
	listener net.Listener
	server   *http.Server

	mu           sync.Mutex
	producerDone bool
	lastRequest  time.Time
}

// New binds an ephemeral port on cfg.LocalIP and starts serving cfg.Dir.
func New(cfg Config) (*Server, error) {
	ln, err := net.Listen("tcp", cfg.LocalIP+":0")
	if err != nil {
		return nil, err
	}

	s := &Server{cfg: cfg, listener: ln, lastRequest: time.Now()}

	files := http.FileServer(http.Dir(cfg.Dir))
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.touch()
		slog.InfoContext(r.Context(), "hls request", "from", r.RemoteAddr, "path", r.URL.Path)
		// Go doesn't register .m3u8/.m4s, so set the type before ServeContent sniffs.
		if ct := contentTypeFor(r.URL.Path); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		files.ServeHTTP(w, r)
	})
	s.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	go func() { _ = s.server.Serve(ln) }()
	return s, nil
}

// URL is the media-playlist address the device should play.
func (s *Server) URL() *url.URL {
	return &url.URL{Scheme: "http", Host: s.listener.Addr().String(), Path: "/" + s.cfg.Playlist}
}

// ProducerDone marks the encoder as exited, letting Wait return once the client
// drains the tail.
func (s *Server) ProducerDone() {
	s.mu.Lock()
	s.producerDone = true
	s.mu.Unlock()
}

func (s *Server) touch() {
	s.mu.Lock()
	s.lastRequest = time.Now()
	s.mu.Unlock()
}

// Wait blocks until the stream is produced and drained, or ctx is cancelled. A
// live source never finishes producing, so it ends on ctx (Ctrl+C).
func (s *Server) Wait(ctx context.Context) error {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		s.mu.Lock()
		finished := s.producerDone && time.Since(s.lastRequest) > idleGrace
		s.mu.Unlock()
		if finished {
			return nil
		}
	}
}

// Close stops the HTTP server.
func (s *Server) Close() error {
	return s.server.Close()
}

// contentTypeFor returns the MIME type for an HLS artifact by extension, or ""
// to let the file server decide.
func contentTypeFor(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".m4s":
		return "video/iso.segment"
	case ".mp4":
		return "video/mp4"
	default:
		return ""
	}
}
