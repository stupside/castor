package transcode

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
)

// StreamServer serves an io.Reader over HTTP. The consumer's read
// pace drives the producer through OS pipe backpressure.
type StreamServer struct {
	reader      io.Reader
	active      atomic.Bool
	contentType string
	extension   string
	headers     map[string]string
	listener    net.Listener
	server      *http.Server
	done        chan struct{}
}

// StreamServerConfig holds the parameters for NewStreamServer.
type StreamServerConfig struct {
	LocalIP     string
	ContentType string
	Extension   string
	Headers     map[string]string
}

func NewStreamServer(cfg StreamServerConfig, reader io.Reader) (*StreamServer, error) {
	ln, err := net.Listen("tcp", cfg.LocalIP+":0")
	if err != nil {
		return nil, err
	}
	s := &StreamServer{
		reader:      reader,
		contentType: cfg.ContentType,
		extension:   cfg.Extension,
		headers:     cfg.Headers,
		listener:    ln,
		done:        make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/stream"+cfg.Extension, s.handleStream)
	s.server = &http.Server{Handler: mux}
	go func() {
		s.server.Serve(ln)
		close(s.done)
	}()
	return s, nil
}

// URL returns the full URL the server is listening on.
func (s *StreamServer) URL() *url.URL {
	return &url.URL{
		Scheme: "http",
		Host:   s.listener.Addr().String(),
		Path:   "/stream" + s.extension,
	}
}

// Close shuts down the server.
func (s *StreamServer) Close() error { return s.server.Close() }

// Wait blocks until the server exits or the context is cancelled.
func (s *StreamServer) Wait(ctx context.Context) error {
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *StreamServer) handleStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", s.contentType)
	for k, v := range s.headers {
		w.Header().Set(k, v)
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	if !s.active.CompareAndSwap(false, true) {
		http.Error(w, "stream already active", http.StatusServiceUnavailable)
		return
	}
	defer s.active.Store(false)

	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	buf := make([]byte, 32*1024)
	readerDone := false
	for {
		n, err := s.reader.Read(buf)
		if n > 0 {
			if _, we := w.Write(buf[:n]); we != nil {
				break // TV disconnected — keep server alive
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err != nil {
			readerDone = true
			break
		}
	}
	if readerDone {
		// Shut down so Wait() unblocks. Goroutine avoids handler deadlock.
		go s.server.Close()
	}
}
