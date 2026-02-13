package transcode

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
)

// StreamBuffer is a thread-safe, append-only buffer that allows multiple
// readers to consume data independently via per-request offsets.
type StreamBuffer struct {
	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
	done bool // true when the source reader is exhausted
}

// NewStreamBuffer creates a new StreamBuffer.
func NewStreamBuffer() *StreamBuffer {
	sb := &StreamBuffer{}
	sb.cond = sync.NewCond(&sb.mu)
	return sb
}

// Ingest reads from r until EOF and appends data to the buffer.
// It should be run in a dedicated goroutine.
func (sb *StreamBuffer) Ingest(r io.Reader) {
	tmp := make([]byte, 32*1024)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			sb.mu.Lock()
			sb.buf = append(sb.buf, tmp[:n]...)
			sb.mu.Unlock()
			sb.cond.Broadcast()
		}
		if err != nil {
			sb.mu.Lock()
			sb.done = true
			sb.mu.Unlock()
			sb.cond.Broadcast()
			return
		}
	}
}

// ReadAt copies buffered data starting at offset into p. It blocks until
// data is available or the source is exhausted. Returns the number of bytes
// copied and io.EOF when all data has been consumed.
func (sb *StreamBuffer) ReadAt(p []byte, offset int) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	for offset >= len(sb.buf) && !sb.done {
		sb.cond.Wait()
	}

	if offset >= len(sb.buf) {
		return 0, io.EOF
	}

	n := copy(p, sb.buf[offset:])
	return n, nil
}

// WaitForData blocks until the buffer contains at least minBytes or the
// context is cancelled.
func (sb *StreamBuffer) WaitForData(ctx context.Context, minBytes int) error {
	done := make(chan struct{})
	go func() {
		sb.mu.Lock()
		for len(sb.buf) < minBytes && !sb.done {
			sb.cond.Wait()
		}
		sb.mu.Unlock()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		// Unblock the waiter goroutine.
		sb.cond.Broadcast()
		return ctx.Err()
	}
}

// StreamServer serves transcode output over HTTP.
type StreamServer struct {
	stream      io.Reader
	buffer      *StreamBuffer
	localIP     string
	contentType string
	extension   string
	listener    net.Listener
	server      *http.Server
	errCh       chan error
}

// NewStreamServer creates a new StreamServer.
func NewStreamServer(stream io.Reader, localIP, contentType, extension string) (*StreamServer, error) {
	ln, err := net.Listen("tcp", localIP+":0")
	if err != nil {
		return nil, fmt.Errorf("listening: %w", err)
	}

	s := &StreamServer{
		stream:      stream,
		buffer:      NewStreamBuffer(),
		localIP:     localIP,
		contentType: contentType,
		extension:   extension,
		listener:    ln,
		errCh:       make(chan error, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/stream"+extension, s.handleStream)

	s.server = &http.Server{Handler: mux}

	return s, nil
}

// Start begins the ingestion goroutine and HTTP server.
func (s *StreamServer) Start() {
	go s.buffer.Ingest(s.stream)

	go func() {
		if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			s.errCh <- err
		}
		close(s.errCh)
	}()
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
	return s.buffer.WaitForData(ctx, minBytes)
}

func (s *StreamServer) handleStream(w http.ResponseWriter, r *http.Request) {
	slog.Info("stream request", "method", r.Method, "remote", r.RemoteAddr, "range", r.Header.Get("Range"))

	w.Header().Set("Content-Type", s.contentType)
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", "DLNA.ORG_OP=00;DLNA.ORG_CI=1;DLNA.ORG_FLAGS=01700000000000000000000000000000")
	w.Header().Set("Accept-Ranges", "none")

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	buf := make([]byte, 32*1024)
	offset := 0
	for {
		n, err := s.buffer.ReadAt(buf, offset)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			offset += n
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
