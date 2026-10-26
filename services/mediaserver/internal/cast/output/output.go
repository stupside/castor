// Package output defines the delivery port consumed by playback orchestration.
// Protocol adapters prepare artifacts, muxer configuration and pacing.
package output

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

// Captions supplies timed cues and their measured position on the media clock.
type Captions struct {
	Cues        *subtitle.Cues
	VideoOffset time.Duration
}

// Output is a serving view of artifacts. Close releases owned storage; the
// caller must first join production and close its HTTP publication.
type Output interface {
	Path() string
	Handler() http.Handler
	Wait(context.Context, <-chan struct{}) error
	Artifact() Artifact
	Audience() health.Audience
	Settled(media.Progress) error
	Close() error
}

// Prepared separates the artifact view from the work required to build it.
// Write borrows its reader and returns when packaging is complete. WatchStorage,
// when present, reports a storage failure; the attempt owns both operations.
type Prepared struct {
	Output       Output
	Write        func(context.Context, io.Reader) error
	WatchStorage func(context.Context) error
}

// Plan binds a supported presentation to the producer format and artifact preparation.
// EncoderFormat can differ from Format when an adapter packages producer bytes.
type Plan interface {
	Format() container.Format
	EncoderFormat() container.Format
	Pace(burning bool) fetch.Pace
	// Splices is whether encoders can take over one after another in what it serves.
	Splices() bool
	Reserve(dir string) (func() error, error)
	Prepare(Opening, string) (Prepared, error)
}

// Relayer is implemented by outputs that can serve an existing buffer without
// starting another encoder. Other protocols need only implement Plan.
type Relayer interface {
	ServeSpool(Opening, *storage.Spool) Output
}

// Continuer is an output that can carry on from another buffer after a byte of its own.
type Continuer interface {
	Continue(at int64, next *storage.Spool) bool
}

// Seeker makes the stream again from at and has the output Restart on it.
type Seeker func(ctx context.Context, at time.Duration) error

// Seekable is an output that serves the stream from a position the device asks for.
type Seekable interface {
	SeekWith(runtime time.Duration, seek Seeker)
	// Restart serves sp, the stream made from began, to every request from now on.
	Restart(began time.Duration, sp *storage.Spool)
}

// Adapter accepts complete transport envelopes. An unsupported caption request
// is declined rather than silently discarded.
type Adapter interface {
	Select(media.FormatSupport, *Captions) (Plan, bool)
}

type Factory []Adapter

// Selection retains the exact envelopes admitted by the selected adapter.
type Selection struct {
	Plan         Plan
	Capabilities media.Capabilities
}

func (f Factory) Select(caps media.Capabilities, captions *Captions) (Selection, error) {
	selections := f.Selections(caps, captions)
	if len(selections) > 0 {
		return selections[0], nil
	}
	return Selection{}, fmt.Errorf("no delivery adapter accepts the requested playback envelopes")
}

// Selections returns adapter-supported envelopes in receiver preference order.
// The codec planner can then try the next delivery when the host cannot encode
// for the first one. Each selection retains only tuples for its transport.
func (f Factory) Selections(caps media.Capabilities, captions *Captions) []Selection {
	var selections []Selection
	seen := make(map[media.Transport]bool)
	for _, envelope := range caps.Formats {
		if seen[envelope.Transport] {
			continue
		}
		for _, adapter := range f {
			plan, ok := adapter.Select(envelope, captions)
			if !ok {
				continue
			}
			selected := caps
			selected.Formats = slices.DeleteFunc(slices.Clone(caps.Formats), func(other media.FormatSupport) bool { return other.Transport != envelope.Transport })
			selections = append(selections, Selection{Plan: plan, Capabilities: selected})
			seen[envelope.Transport] = true
			break
		}
	}
	return selections
}

// Artifact is what a delivery hands the device, which the watch that opens it waits on.
type Artifact struct {
	// Path names the local artifact for probing without issuing receiver requests.
	Path string
	// Subject names it in a log line and in a fault ("the stream output", "the HLS playlist").
	Subject string

	Landed func() (int64, error)

	Grace time.Duration
}

// Opening is everything a delivery mechanism is opened with.
type Opening struct {
	Live    bool
	Format  container.Format
	Headers map[string]string

	// IdleGrace is how long an idle device is waited for before the delivery counts as done.
	IdleGrace time.Duration

	// WriteDeadline bounds one socket write, so a hung client cannot hold its goroutine.
	WriteDeadline time.Duration
}

// Answering serves each request with the device's headers under a write deadline, counting what it sent.
func (o Opening) Answering(serve func(*Answer, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Not lifted here: net/http lifts it after flushing the buffered answer, which it must still cover.
		if o.WriteDeadline > 0 {
			if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(o.WriteDeadline)); err != nil {
				slog.WarnContext(r.Context(), "write deadline unavailable", "error", err)
			}
		}
		for k, v := range o.Headers {
			w.Header().Set(k, v)
		}
		serve(&Answer{ResponseWriter: w}, r)
	}
}

// Answer records the status and bytes a ResponseWriter does not report.
type Answer struct {
	http.ResponseWriter
	Status int
	Bytes  int64
}

func (a *Answer) WriteHeader(code int) {
	if a.Status == 0 {
		a.Status = code
	}
	a.ResponseWriter.WriteHeader(code)
}

func (a *Answer) Write(b []byte) (int, error) {
	if a.Status == 0 {
		a.Status = http.StatusOK
	}
	n, err := a.ResponseWriter.Write(b)
	a.Bytes += int64(n)
	return n, err
}

// ReadFrom keeps the connection's own ReaderFrom, so a file server sends without a userspace copy.
func (a *Answer) ReadFrom(r io.Reader) (int64, error) {
	if a.Status == 0 {
		a.Status = http.StatusOK
	}
	n, err := io.Copy(a.ResponseWriter, r)
	a.Bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the connection's own flush and deadlines.
func (a *Answer) Unwrap() http.ResponseWriter { return a.ResponseWriter }

// Carried is an answer that succeeded and handed over bytes.
func (a *Answer) Carried() bool { return a.Status >= 200 && a.Status < 300 && a.Bytes > 0 }

const settleInterval = 500 * time.Millisecond

// Settle waits for production to drain and the receiver to finish consuming it.
func Settle(ctx context.Context, drained <-chan struct{}, finished func() bool) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-drained:
	}
	tick := time.NewTicker(settleInterval)
	defer tick.Stop()
	for !finished() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	return nil
}
