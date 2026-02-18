package transcode

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/smallnest/ringbuffer"
)

const defaultReadBufSize = 32 * 1024 / 2

// StreamServer serves transcode output over HTTP using a blocking ring
// buffer. Write blocks when full, applying backpressure to ffmpeg so
// no data is ever silently overwritten.
type StreamServer struct {
	ring        *ringbuffer.RingBuffer
	cancel      context.CancelFunc
	active      atomic.Bool
	contentType string
	extension   string
	headers     map[string]string
	listener    net.Listener
	server      *http.Server
	errCh       chan error
}

// StreamServerConfig holds configuration for creating a StreamServer.
type StreamServerConfig struct {
	LocalIP        string
	ContentType    string
	Extension      string
	Headers        map[string]string
	BufferCapacity int
}

// NewStreamServer creates, starts, and returns a StreamServer. It begins
// ingesting from stream and serving HTTP immediately.
func NewStreamServer(ctx context.Context, cfg StreamServerConfig, stream io.Reader) (*StreamServer, error) {
	ln, err := net.Listen("tcp", cfg.LocalIP+":0")
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)

	s := &StreamServer{
		ring:        ringbuffer.New(cfg.BufferCapacity).SetBlocking(true),
		cancel:      cancel,
		contentType: cfg.ContentType,
		extension:   cfg.Extension,
		headers:     cfg.Headers,
		listener:    ln,
		errCh:       make(chan error, 1),
	}

	s.ring.WithCancel(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/stream"+cfg.Extension, s.handleStream)

	s.server = &http.Server{Handler: mux}

	go func() {
		s.ring.ReadFrom(stream)
		s.ring.CloseWriter()
	}()

	go func() {
		if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			s.errCh <- err
		}
		close(s.errCh)
	}()

	return s, nil
}

// Stop cancels ingestion and closes the server.
func (s *StreamServer) Stop() {
	s.cancel()
	s.server.Close()
}

// URL returns the full URL the server is listening on.
func (s *StreamServer) URL() *url.URL {
	return &url.URL{
		Scheme: "http",
		Host:   s.listener.Addr().String(),
		Path:   "/stream" + s.extension,
	}
}

// Wait blocks until the server exits or the context is cancelled.
func (s *StreamServer) Wait(ctx context.Context) error {
	select {
	case err := <-s.errCh:
		return err
	case <-ctx.Done():
		return nil
	}
}

// WaitForData blocks until the buffer has at least minBytes of data or
// the context is cancelled.
func (s *StreamServer) WaitForData(ctx context.Context, minBytes int) error {
	const tick = 50 * time.Millisecond
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		if s.ring.Length() >= minBytes {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *StreamServer) handleStream(w http.ResponseWriter, r *http.Request) {
	if !s.active.CompareAndSwap(false, true) {
		http.Error(w, "stream already has an active reader", http.StatusServiceUnavailable)
		return
	}
	defer s.active.Store(false)

	w.Header().Set("Content-Type", s.contentType)
	for k, v := range s.headers {
		w.Header().Set(k, v)
	}

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	buf := make([]byte, defaultReadBufSize)

	for {
		n, err := s.ring.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
