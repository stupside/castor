package transcode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
)

// StreamServer serves transcode output over HTTP using a fixed-size ring
// buffer instead of an unbounded append-only buffer.
type StreamServer struct {
	ring        *ringBuffer
	pool        *sync.Pool
	localIP     string
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
	ReadBufSize    int
}

// NewStreamServer creates a new StreamServer. The stream data source is
// provided later via Start.
func NewStreamServer(cfg StreamServerConfig) (*StreamServer, error) {
	ln, err := net.Listen("tcp", cfg.LocalIP+":0")
	if err != nil {
		return nil, fmt.Errorf("listening: %w", err)
	}

	s := &StreamServer{
		ring: newRingBuffer(cfg.BufferCapacity),
		pool: &sync.Pool{
			New: func() any {
				b := make([]byte, cfg.ReadBufSize)
				return &b
			},
		},
		localIP:     cfg.LocalIP,
		contentType: cfg.ContentType,
		extension:   cfg.Extension,
		headers:     cfg.Headers,
		listener:    ln,
		errCh:       make(chan error, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/stream"+cfg.Extension, s.handleStream)

	s.server = &http.Server{Handler: mux}

	return s, nil
}

// Start begins the ingestion goroutine and HTTP server.
func (s *StreamServer) Start(stream io.Reader) {
	go s.ingest(stream)

	go func() {
		if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			s.errCh <- err
		}
		close(s.errCh)
	}()
}

// ingest reads from the stream source and writes into the ring buffer.
func (s *StreamServer) ingest(r io.Reader) {
	bp := s.pool.Get().(*[]byte)
	defer s.pool.Put(bp)

	var closeErr error
	for {
		n, err := r.Read(*bp)
		if n > 0 {
			s.ring.Write((*bp)[:n])
		}
		if err != nil {
			if err != io.EOF {
				closeErr = err
			}
			break
		}
	}
	s.ring.Close(closeErr)
}

// Stop forcibly closes the server and all active connections.
func (s *StreamServer) Stop() {
	s.server.Close()
}

// URL returns the full URL the server is listening on.
func (s *StreamServer) URL() (*url.URL, error) {
	addr := s.listener.Addr().String()
	return url.Parse(fmt.Sprintf("http://%s/stream%s", addr, s.extension))
}

// Err returns a channel that receives any server error.
func (s *StreamServer) Err() <-chan error {
	return s.errCh
}

// WaitForData blocks until the buffer has at least minBytes of data or
// the context is cancelled.
func (s *StreamServer) WaitForData(ctx context.Context, minBytes int) error {
	return s.ring.WaitForData(ctx, int64(minBytes))
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

	w.WriteHeader(http.StatusOK)

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	ctx := r.Context()

	bp := s.pool.Get().(*[]byte)
	defer s.pool.Put(bp)

	var offset int64
	for {
		n, err := s.ring.ReadAt(ctx, *bp, offset)
		if n > 0 {
			if _, writeErr := w.Write((*bp)[:n]); writeErr != nil {
				return
			}
			offset += int64(n)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err != nil {
			if errors.Is(err, errDataOverwritten) {
				offset = s.ring.OldestOffset()
				slog.Warn("stream reader fell behind, skipping ahead", "new_offset", offset)
				continue
			}
			return
		}
	}
}
