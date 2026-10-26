package progressive

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stupside/castor/internal/telemetry"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/cast/teardown"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

const (
	// sendChunkSize is the per-connection read/write granularity.
	sendChunkSize = 32 * 1024

	// Startup timeout; generous because it catches early container failures.
	firstBytesTimeout = 10 * time.Second
)

// Stream serves a spool. Its owner runs Write, publishes Handler, and closes
// the spool only after writers and requests have stopped.
type Stream struct {
	o        output.Opening
	spool    *storage.Spool
	owned    bool
	audience streamAudience
	run      atomic.Pointer[run]

	// seek is nil where the stream is served from its start only.
	seek    output.Seeker
	runtime time.Duration
}

// run is the stream as served since it last began.
type run struct {
	spool *storage.Spool
	next  *successor
	began time.Duration
	gen   int
}

// successor is the buffer the stream carries on with from byte at.
type successor struct {
	at    int64
	spool *storage.Spool
}

// errRestarted ends a client of a run that a seek replaced.
var errRestarted = errors.New("the stream began again from another position")

// Continue serves next after byte at, false where a client was written too close to at.
func (s *Stream) Continue(at int64, next *storage.Spool) bool {
	r := s.run.Load()
	if handed, _ := s.audience.handed(); r.next != nil || at < handed+cutMargin {
		return false
	}
	carried := *r
	carried.next = &successor{at: at, spool: next}
	return s.run.CompareAndSwap(r, &carried)
}

// SeekWith lets a device ask for the stream from a position within runtime.
func (s *Stream) SeekWith(runtime time.Duration, seek output.Seeker) {
	s.runtime, s.seek = runtime, seek
}

// Began is where in the program the current run begins.
func (s *Stream) Began() time.Duration { return s.run.Load().began }

// Restart serves sp, made from began, to every request from now on.
func (s *Stream) Restart(began time.Duration, sp *storage.Spool) {
	// The audience moves first, so a client of the run it leaves credits nothing of the next.
	s.run.Store(&run{spool: sp, began: began, gen: s.audience.restart()})
}

// cutMargin is how far past what any client was written a cut must lie, so no client is mid-write across it.
const cutMargin = 128 << 10

// snapshot counts the buffer the stream carries on with.
func (s *Stream) snapshot() (int64, bool) {
	r := s.run.Load()
	size, final := r.spool.Snapshot()
	if r.next != nil {
		tail, tailFinal := r.next.spool.Snapshot()
		return r.next.at + tail, tailFinal
	}
	return size, final
}

// tailAt reads the current run from offset, across into its successor.
func (s *Stream) tailAt(ctx context.Context, offset int64) (*joinedTail, error) {
	r := s.run.Load()
	t := &joinedTail{s: s, run: r, ctx: ctx, pos: offset}
	if r.next != nil && offset >= r.next.at {
		t.switched = true
		cur, err := r.next.spool.TailAt(ctx, offset-r.next.at)
		t.cur = cur
		return t, err
	}
	cur, err := r.spool.TailAt(ctx, offset)
	t.cur = cur
	return t, err
}

// joinedTail fails with errRestarted once a seek replaces its run.
type joinedTail struct {
	s        *Stream
	run      *run
	ctx      context.Context
	cur      io.ReadCloser
	pos      int64
	switched bool
}

func (t *joinedTail) current() (*run, bool) {
	r := t.s.run.Load()
	return r, r.gen == t.run.gen
}

func (t *joinedTail) Read(p []byte) (int, error) {
	r, ok := t.current()
	if !ok {
		return 0, errRestarted
	}
	if next := r.next; next != nil && !t.switched {
		if t.pos >= next.at {
			if err := t.carryOn(next); err != nil {
				return 0, err
			}
		} else {
			p = p[:min(int64(len(p)), next.at-t.pos)]
		}
	}
	n, err := t.cur.Read(p)
	t.pos += int64(n)
	if _, ok := t.current(); !ok && err != nil {
		return n, errRestarted
	}
	return n, err
}

func (t *joinedTail) carryOn(next *successor) error {
	_ = t.cur.Close()
	cur, err := next.spool.TailAt(t.ctx, t.pos-next.at)
	t.cur, t.switched = cur, true
	return err
}

func (t *joinedTail) Close() error {
	if t.cur == nil {
		return nil
	}
	return t.cur.Close()
}

func NewStream(o output.Opening, dir string) (*Stream, error) {
	openSpool := storage.NewSpool
	if o.Live {
		openSpool = storage.NewLiveSpool
	}
	sp, err := openSpool(filepath.Join(dir, "out"+o.Format.Extension))
	if err != nil {
		return nil, fmt.Errorf("opening stream spool: %w", err)
	}
	s := ServeSpool(o, sp)
	s.owned = true
	return s, nil
}

// ServeSpool borrows sp; the caller retains ownership of its writer and storage.
func ServeSpool(o output.Opening, sp *storage.Spool) *Stream {
	s := &Stream{o: o, spool: sp, audience: streamAudience{lastDisconnect: time.Now()}}
	s.run.Store(&run{spool: sp})
	return s
}

// Write consumes producer synchronously. The caller owns and interrupts the reader.
func (s *Stream) Write(ctx context.Context, producer io.Reader) error {
	_, err := io.Copy(s.spool, producer)
	err = teardown.Failures(ctx, err)
	// A stopped write is truncated: a killed producer ends its copy as cleanly as a finished one.
	s.spool.CloseWrite(cmp.Or(err, ctx.Err()))
	return err
}

func (s *Stream) Path() string { return "stream" + s.o.Format.Extension }
func (s *Stream) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /"+s.Path(), s.handleStream)
	return mux
}

// Handed returns most bytes to any one client and when byte last moved; safe over-count.
func (s *Stream) Handed() (int64, time.Time) {
	return s.audience.handed()
}

func (s *Stream) Artifact() output.Artifact {
	return output.Artifact{
		Path:    s.spool.ProbePath(),
		Subject: "the stream output",
		Landed:  func() (int64, error) { n, _ := s.snapshot(); return n, nil },
		Grace:   firstBytesTimeout,
	}
}

func (s *Stream) Close() error {
	if s.owned {
		return s.spool.Close()
	}
	return nil
}

func (s *Stream) Wait(ctx context.Context, produced <-chan struct{}) error {
	err := output.Settle(ctx, produced, func() bool { return s.audience.settled(s.o.IdleGrace) })
	return errors.Join(err, s.Err())
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
	if s.run.Load().spool.Live() {
		return s.resumeLive(rangeHeader), ""
	}
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
	total, final := s.snapshot()
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

// severed says why one client stopped being written to.
func (s *Stream) severed(ctx context.Context, r *http.Request, err error, held int64, stalled time.Duration) {
	// A cancelled request zeroes the write deadline to wake the writer, so that error is the client leaving, not a stall.
	leaving := ctx.Err() != nil || !errors.Is(err, os.ErrDeadlineExceeded)
	outcome := telemetry.OutcomeStalled
	if leaving {
		outcome = telemetry.OutcomeDisconnected
	}
	telemetry.StreamClientEnded(ctx, outcome)
	if leaving {
		slog.InfoContext(ctx, "stream client disconnected", "from", r.RemoteAddr, "bytes_sent", held, "error", err)
		return
	}
	_, final := s.snapshot()
	slog.WarnContext(ctx, "severed a client that stopped draining the stream; unless it reconnects with a Range it restarts the program from the beginning",
		"from", r.RemoteAddr,
		"user_agent", r.UserAgent(),
		"stalled_for", stalled.Round(time.Millisecond),
		"write_deadline", s.o.WriteDeadline,
		"bytes_sent", held,
		"resumable", final && !s.rangesDeclined(),
	)
}

func (s *Stream) handleStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	s.writeHeaders(w)
	if r.Method == http.MethodHead {
		slog.InfoContext(ctx, "stream HEAD", "from", r.RemoteAddr, "user_agent", r.UserAgent())
		w.WriteHeader(http.StatusOK)
		return
	}

	// A client is counted from its request, so a seek that takes a while does not leave the stream looking abandoned.
	reachedEOF, gen := false, -1
	left := s.audience.joined()
	defer func() { left(gen, reachedEOF) }()

	var sv served
	if asked := r.Header.Get(timeSeekHeader); asked != "" {
		var ok bool
		if sv, ok = s.seekTo(ctx, w, r, asked); !ok {
			return
		}
	} else {
		var ok bool
		if sv, ok = s.admit(ctx, w, r); !ok {
			return
		}
	}

	tail, err := s.tailAt(ctx, sv.start)
	if err != nil {
		if errors.Is(err, storage.ErrSpoolExpired) {
			http.Error(w, err.Error(), http.StatusGone)
		} else {
			http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	defer tail.Close()
	gen = tail.run.gen

	if err := s.respond(w, sv); err != nil {
		s.severed(ctx, r, err, sv.start, 0)
		return
	}
	reachedEOF = s.send(ctx, w, r, sv, tail, gen)
}

func (s *Stream) writeHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", s.o.Format.ContentType)
	for k, v := range s.o.Headers {
		w.Header().Set(k, v)
	}
}

// timeSeekHeader is how a DLNA renderer asks for a stream from a position in time.
const timeSeekHeader = "TimeSeekRange.dlna.org"

// seekTo has the stream begin again at the position asked, for this request and every one after it.
func (s *Stream) seekTo(ctx context.Context, w http.ResponseWriter, r *http.Request, asked string) (served, bool) {
	at, ok := nptStart(asked)
	slog.InfoContext(ctx, "stream seek", "from", r.RemoteAddr, "user_agent", r.UserAgent(), "asked", asked, "at", at)
	switch {
	case s.seek == nil:
		http.Error(w, "this stream is served from its start only", http.StatusNotAcceptable)
		return served{}, false
	case !ok || at >= s.runtime:
		http.Error(w, "the position asked for is not in the stream", http.StatusRequestedRangeNotSatisfiable)
		return served{}, false
	}
	if err := s.seek(ctx, at); err != nil {
		slog.WarnContext(ctx, "the stream could not be made from the position asked for", "at", at, "error", err)
		http.Error(w, "the stream could not be made from there", http.StatusServiceUnavailable)
		return served{}, false
	}
	// The stream begins at the keyframe the source has there, which the answer states.
	w.Header().Set(timeSeekHeader, "npt="+nptSeconds(s.Began())+"-"+nptSeconds(s.runtime)+"/"+nptSeconds(s.runtime))
	return served{code: http.StatusOK, start: 0, end: -1, total: -1}, true
}

// nptStart is the start of a TimeSeekRange npt range, in seconds (12.5) or as H:MM:SS(.f).
func nptStart(header string) (time.Duration, bool) {
	spec, found := strings.CutPrefix(strings.TrimSpace(header), "npt=")
	start, _, ranged := strings.Cut(spec, "-")
	if !found || !ranged {
		return 0, false
	}
	parts := strings.Split(strings.TrimSpace(start), ":")
	if len(parts) > 3 {
		return 0, false
	}
	var seconds float64
	for _, part := range parts {
		v, err := strconv.ParseFloat(part, 64)
		if err != nil || v < 0 || math.IsInf(v, 0) || math.IsNaN(v) {
			return 0, false
		}
		seconds = seconds*60 + v
	}
	// Anything past a year is no position in a cast, and would overflow a duration.
	if seconds > (365 * 24 * time.Hour).Seconds() {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func nptSeconds(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) }

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
	if sv.code == http.StatusGone {
		first, end := s.run.Load().spool.Retained()
		w.Header().Set("X-Castor-Retained-Bytes", fmt.Sprintf("%d-%d", first, end))
		http.Error(w, storage.ErrSpoolExpired.Error(), sv.code)
		return sv, false
	}
	if sv.code == http.StatusRequestedRangeNotSatisfiable {
		if sv.total >= 0 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", sv.total))
		}
		http.Error(w, "requested range not satisfiable", sv.code)
		return sv, false
	}
	return sv, true
}

func (s *Stream) respond(w http.ResponseWriter, sv served) error {
	if sv.code == http.StatusPartialContent {
		total := "*"
		if sv.total >= 0 {
			total = strconv.FormatInt(sv.total, 10)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%s", sv.start, sv.end, total))
		w.Header().Set("Content-Length", strconv.FormatInt(sv.end-sv.start+1, 10))
	}
	w.WriteHeader(sv.code)
	return http.NewResponseController(w).Flush()
}

// send credits the audience only while gen is the current run.
func (s *Stream) send(ctx context.Context, w http.ResponseWriter, r *http.Request, sv served, tail io.Reader, gen int) (reachedEOF bool) {
	// remaining is how many bytes this response still owes, and -1 when it owes the rest of the stream.
	remaining := int64(-1)
	if sv.end >= 0 {
		remaining = sv.end - sv.start + 1
	}

	control := http.NewResponseController(w)
	buf := make([]byte, sendChunkSize)
	var sent int64
	defer func() { telemetry.StreamSent(ctx, sent) }()
	lastWrite := time.Now()
	deadlines := s.o.WriteDeadline > 0
	for {
		if ctx.Err() != nil {
			return
		}
		want := buf
		if remaining >= 0 {
			want = buf[:min(int64(len(buf)), remaining)]
		}
		n, readErr := tail.Read(want)
		if n > 0 {
			if deadlines {
				if err := control.SetWriteDeadline(time.Now().Add(s.o.WriteDeadline)); err != nil {
					deadlines = false
					slog.WarnContext(ctx, "stream write deadline unavailable, a client that stops reading will park this goroutine", "error", err)
				}
			}
			// A cancellation deadline may have fired before the write deadline
			// was refreshed. Never write after extending a canceled request's deadline.
			if ctx.Err() != nil {
				return
			}
			if _, err := w.Write(want[:n]); err != nil {
				s.severed(ctx, r, err, sv.start+sent, time.Since(lastWrite))
				return
			}
			if err := control.Flush(); err != nil {
				s.severed(ctx, r, err, sv.start+sent, time.Since(lastWrite))
				return
			}
			sent += int64(n)
			lastWrite = time.Now()
			if remaining >= 0 {
				remaining -= int64(n)
			}
			// A resumed connection is credited with the prefix it already had.
			s.audience.record(gen, sv.start+sent, lastWrite)
		}
		if remaining == 0 {
			// The stretch asked for is delivered.
			reachedEOF = sv.total >= 0 && sv.end == sv.total-1
			slog.InfoContext(ctx, "stream range delivered",
				"from", r.RemoteAddr, "bytes_sent", sent, "first_byte", sv.start, "last_byte", sv.end)
			return
		}
		if readErr != nil {
			if errors.Is(readErr, errRestarted) {
				slog.InfoContext(ctx, "stream client left behind by a seek", "from", r.RemoteAddr, "bytes_sent", sent)
				return
			}
			if readErr == io.EOF {
				reachedEOF = true
				slog.InfoContext(ctx, "stream fully delivered", "from", r.RemoteAddr, "bytes_sent", sent)
			} else {
				slog.InfoContext(ctx, "stream read ended", "from", r.RemoteAddr, "bytes_sent", sent, "error", readErr)
				if errors.Is(readErr, storage.ErrSpoolExpired) {
					panic(http.ErrAbortHandler)
				}
			}
			return
		}
	}
}

// Err is the producer failure retained by this delivery.
func (s *Stream) Err() error {
	r := s.run.Load()
	err := r.spool.Err()
	if r.next != nil {
		err = errors.Join(err, r.next.spool.Err())
	}
	return err
}

func (s *Stream) Audience() health.Audience { return s }
func (s *Stream) Settled(made media.Progress) error {
	if err := s.Err(); err != nil {
		return err
	}
	handed, _ := s.Handed()
	return health.Shortfall(handed, made)
}
