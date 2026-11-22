package progressive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/stupside/castor/internal/telemetry"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
)

// disconnected records a client that stopped being written to: it left, or the cast ended.
func disconnected(ctx context.Context, r *http.Request, err error, held int64) {
	telemetry.StreamClientEnded(ctx, telemetry.OutcomeDisconnected)
	slog.InfoContext(ctx, "stream client disconnected", "from", r.RemoteAddr, "bytes_sent", held, "error", err)
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
		disconnected(ctx, r, err, sv.start)
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
	// No write deadline: a device its viewer paused stops reading for as long as it likes, and only the cast's watch judges a quiet device.
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
			if _, err := w.Write(want[:n]); err != nil {
				disconnected(ctx, r, err, sv.start+sent)
				return
			}
			if err := control.Flush(); err != nil {
				disconnected(ctx, r, err, sv.start+sent)
				return
			}
			sent += int64(n)
			if remaining >= 0 {
				remaining -= int64(n)
			}
			// A resumed connection is credited with the prefix it already had.
			s.audience.record(gen, sv.start+sent, time.Now())
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
