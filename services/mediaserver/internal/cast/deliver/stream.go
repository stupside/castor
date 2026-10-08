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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// sendChunkSize is the per-connection read/write granularity.
	sendChunkSize = 32 * 1024

	// Startup timeout; generous because it catches early container failures.
	firstBytesTimeout = 10 * time.Second
)

// Stream spools producer output and replays from byte 0; URL after first byte/timeout.
type Stream struct {
	o Opening

	listener net.Listener
	server   *http.Server
	cancel   context.CancelFunc
	spool    *Spool

	done <-chan struct{} // producer fully spooled
	// reading is closed once this server's own copy of the producer returns; nil where another writes the spool.
	reading <-chan struct{}

	mu             sync.Mutex
	active         int
	completed      bool // some client consumed the stream to EOF
	lastDisconnect time.Time
	// Most bytes any client took; when byte last moved; answers 'did device fetch'.
	sent      int64
	lastFetch time.Time
}

// OpenStream spools producer into dir in the background and serves that spool over HTTP, replaying from byte 0.
func OpenStream(ctx context.Context, o Opening, dir string, producer io.Reader) (*Stream, error) {
	sp, err := NewSpool(filepath.Join(dir, "out"+o.Format.Extension))
	if err != nil {
		return nil, fmt.Errorf("starting stream server: %w", err)
	}
	done := make(chan struct{})
	s, err := listenStream(ctx, o, sp, done)
	if err != nil {
		sp.CloseWrite(nil)
		return nil, fmt.Errorf("starting stream server: %w", err)
	}
	s.reading = done

	go func() {
		defer close(done)
		_, copyErr := io.Copy(sp, producer)
		sp.CloseWrite(copyErr)
		slog.DebugContext(ctx, "stream fully spooled", "bytes", sp.Size(), "error", copyErr)
	}()
	return s, nil
}

// OpenSpooledStream serves a spool another writes, whole once drained closes; Close leaves that writer running.
func OpenSpooledStream(ctx context.Context, o Opening, sp *Spool, drained <-chan struct{}) (*Stream, error) {
	srv, err := listenStream(ctx, o, sp, drained)
	if err != nil {
		return nil, fmt.Errorf("starting stream server: %w", err)
	}
	return srv, nil
}

// listenStream binds where o's listeners say and serves sp, final once done closes.
func listenStream(parent context.Context, o Opening, sp *Spool, done <-chan struct{}) (*Stream, error) {
	ln, err := o.Listeners.Listen(parent)
	if err != nil {
		return nil, err
	}

	// Close alone ends the stream; the cast lends only its values, so its lines reach its watchers.
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	s := &Stream{
		o:              o,
		listener:       ln,
		cancel:         cancel,
		spool:          sp,
		done:           done,
		lastDisconnect: time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /stream"+o.Format.Extension, func(w http.ResponseWriter, r *http.Request) {
		s.handleStream(ctx, w, r)
	})
	s.server = serve(ctx, ln, mux)
	return s, nil
}

func (s *Stream) URL() *url.URL {
	return &url.URL{Scheme: "http", Host: s.listener.Addr().String(), Path: "/stream" + s.o.Format.Extension}
}

// Drained is closed when producer ended and spool has all output.
func (s *Stream) Drained() <-chan struct{} { return s.done }

// Handed returns most bytes to any one client and when byte last moved; safe over-count.
func (s *Stream) Handed() (int64, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent, s.lastFetch
}

func (s *Stream) Artifact() Artifact {
	return Artifact{
		Subject: "the stream output",
		Landed:  func() int64 { n, _ := s.spooled(); return n },
		Grace:   firstBytesTimeout,
	}
}

func (s *Stream) Close() error {
	s.cancel()
	err := stop(s.server)
	if s.reading != nil {
		<-s.reading
	}
	return err
}

func (s *Stream) Wait(ctx context.Context) error {
	return settle(ctx, s.done, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.active == 0 && (s.completed || time.Since(s.lastDisconnect) > s.o.IdleGrace)
	})
}

// served is the stretch of the stream one response carries, and the status that states it.
type served struct {
	// code is 200 (the whole stream from byte 0), 206 or 416 (a start past a known end).
	code int
	// start is the first byte this response carries, end the last one inclusive.
	start int64
	end   int64
	total int64
}

func (s *Stream) resume(rangeHeader string) (served, string) {
	whole := served{code: http.StatusOK, start: 0, end: -1, total: -1}
	if rangeHeader == "" {
		return whole, ""
	}
	start, end, ok := byteRange(rangeHeader)
	if !ok {
		return whole, "the Range header is not a single bytes=first-last range"
	}
	if start == 0 && end < 0 {
		// The ordinary replay: the client is asking for exactly the bytes it is about to be given.
		return whole, ""
	}
	refuse := func(why string) (served, string) {
		if start == 0 {
			return whole, ""
		}
		return whole, why
	}
	if s.rangesDeclined() {
		return refuse("this delivery's own response headers declare Accept-Ranges: none")
	}
	total, final := s.spooled()
	switch {
	case !final:
		return refuse("the producer is still running, so no response can state where this stream ends")
	case start >= total:
		return served{code: http.StatusRequestedRangeNotSatisfiable, total: total}, ""
	case end >= 0:
		return served{code: http.StatusPartialContent, start: start, end: min(end, total-1), total: total}, ""
	default:
		return served{code: http.StatusPartialContent, start: start, end: total - 1, total: total}, ""
	}
}

// byteRange parses the one form a resume takes: a single "bytes=first-last", with the last byte optional.
func byteRange(header string) (start, end int64, ok bool) {
	spec, found := strings.CutPrefix(strings.TrimSpace(header), "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false
	}
	first, last, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(first), 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false
	}
	if strings.TrimSpace(last) == "" {
		return start, -1, true
	}
	end, err = strconv.ParseInt(strings.TrimSpace(last), 10, 64)
	if err != nil || end < start {
		return 0, 0, false
	}
	return start, end, true
}

func (s *Stream) rangesDeclined() bool {
	for k, v := range s.o.Headers {
		if http.CanonicalHeaderKey(k) == "Accept-Ranges" {
			return strings.EqualFold(strings.TrimSpace(v), "none")
		}
	}
	return false
}

// spooled is how many bytes the producer has written and whether that figure is final.
func (s *Stream) spooled() (int64, bool) {
	select {
	case <-s.done:
		return s.spool.Size(), true
	default:
		return s.spool.Size(), false
	}
}

// severed says why one client stopped being written to.
func (s *Stream) severed(ctx context.Context, r *http.Request, err error, held int64, stalled time.Duration) {
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		slog.InfoContext(ctx, "stream client disconnected", "from", r.RemoteAddr, "bytes_sent", held, "error", err)
		return
	}
	_, final := s.spooled()
	slog.WarnContext(ctx, "severed a client that stopped draining the stream; unless it reconnects with a Range it restarts the program from the beginning",
		"from", r.RemoteAddr,
		"user_agent", r.UserAgent(),
		"stalled_for", stalled.Round(time.Millisecond),
		"write_deadline", s.o.WriteDeadline,
		"bytes_sent", held,
		"resumable", final && !s.rangesDeclined(),
	)
}

func (s *Stream) handleStream(srvCtx context.Context, w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(srvCtx, cancel)
	defer stop()

	s.writeHeaders(w)
	if r.Method == http.MethodHead {
		slog.InfoContext(ctx, "stream HEAD", "from", r.RemoteAddr, "user_agent", r.UserAgent())
		w.WriteHeader(http.StatusOK)
		return
	}

	sv, ok := s.admit(ctx, w, r)
	if !ok {
		return
	}

	tail, err := s.spool.TailAt(ctx, sv.start)
	if err != nil {
		http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
		return
	}
	defer tail.Close()

	reachedEOF := false
	left := s.joined()
	defer func() { left(reachedEOF) }()

	flusher := s.respond(w, sv)
	reachedEOF = s.send(ctx, w, r, sv, tail, flusher)
}

func (s *Stream) writeHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", s.o.Format.ContentType)
	for k, v := range s.o.Headers {
		w.Header().Set(k, v)
	}
}

func (s *Stream) admit(ctx context.Context, w http.ResponseWriter, r *http.Request) (served, bool) {
	asked := r.Header.Get("Range")
	sv, refused := s.resume(asked)
	slog.InfoContext(ctx, "stream GET",
		"from", r.RemoteAddr,
		"user_agent", r.UserAgent(),
		"range", asked,
		"status", sv.code,
		"first_byte", sv.start,
	)
	if refused != "" {
		slog.WarnContext(ctx, "a client asked to continue the stream and is being replayed from the beginning instead",
			"from", r.RemoteAddr, "user_agent", r.UserAgent(), "range", asked, "reason", refused)
	}
	if sv.code == http.StatusRequestedRangeNotSatisfiable {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", sv.total))
		http.Error(w, "requested range not satisfiable", sv.code)
		return sv, false
	}
	return sv, true
}

func (s *Stream) joined() func(reachedEOF bool) {
	s.mu.Lock()
	s.active++
	s.mu.Unlock()
	return func(reachedEOF bool) {
		s.mu.Lock()
		s.active--
		s.completed = s.completed || reachedEOF
		s.lastDisconnect = time.Now()
		s.mu.Unlock()
	}
}

func (s *Stream) respond(w http.ResponseWriter, sv served) http.Flusher {
	if sv.code == http.StatusPartialContent {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", sv.start, sv.end, sv.total))
		w.Header().Set("Content-Length", strconv.FormatInt(sv.end-sv.start+1, 10))
	}
	w.WriteHeader(sv.code)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	return flusher
}

func (s *Stream) send(ctx context.Context, w http.ResponseWriter, r *http.Request, sv served, tail io.Reader, flusher http.Flusher) (reachedEOF bool) {
	// remaining is how many bytes this response still owes, and -1 when it owes the rest of the stream.
	remaining := int64(-1)
	if sv.end >= 0 {
		remaining = sv.end - sv.start + 1
	}

	control := http.NewResponseController(w)
	buf := make([]byte, sendChunkSize)
	var sent int64
	lastWrite := time.Now()
	for {
		want := buf
		if remaining >= 0 {
			want = buf[:min(int64(len(buf)), remaining)]
		}
		n, readErr := tail.Read(want)
		if n > 0 {
			if err := control.SetWriteDeadline(time.Now().Add(s.o.WriteDeadline)); err != nil {
				slog.WarnContext(ctx, "stream write deadline unavailable, a client that stops reading will park this goroutine", "error", err)
			}
			if _, err := w.Write(want[:n]); err != nil {
				s.severed(ctx, r, err, sv.start+sent, time.Since(lastWrite))
				return
			}
			sent += int64(n)
			lastWrite = time.Now()
			if remaining >= 0 {
				remaining -= int64(n)
			}
			s.mu.Lock()
			s.lastFetch = lastWrite
			// A resumed connection is credited with the prefix it said it already had.
			s.sent = max(s.sent, sv.start+sent)
			s.mu.Unlock()
			if flusher != nil {
				flusher.Flush()
			}
		}
		if remaining == 0 {
			// The stretch asked for is delivered.
			reachedEOF = sv.total >= 0 && sv.end == sv.total-1
			slog.InfoContext(ctx, "stream range delivered",
				"from", r.RemoteAddr, "bytes_sent", sent, "first_byte", sv.start, "last_byte", sv.end)
			return
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
