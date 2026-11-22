package progressive

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"time"

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
