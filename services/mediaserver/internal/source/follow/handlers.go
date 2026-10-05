package follow

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// segment serves one listed segment: TS or WebM as the origin sent it, fMP4 as MPEG-TS, each decrypted first.
func (f *feed) segment(w http.ResponseWriter, r *http.Request) {
	sequence, err := strconv.ParseInt(strings.TrimSuffix(r.PathValue("sequence"), repackagedExtension), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !servable(w, r) {
		return
	}
	f.mu.Lock()
	s, listed := f.ledger.Entry(sequence)
	repackaging := f.repackaging != nil && *f.repackaging
	f.mu.Unlock()
	// Trimmed: the reader skips it as expired, which is what it is.
	if !listed {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	if s.Map != nil && repackaging {
		f.repackaged(ctx, w, s)
		return
	}
	body, err := f.read(ctx, s)
	if err != nil {
		failed(w, err)
		return
	}
	defer func() { _ = body.Close() }()
	f.answer(ctx, w, "application/octet-stream", s, "relay", func(out io.Writer) error {
		_, err := io.Copy(out, body)
		return err
	})
}

// answer serves what write produces; a failure before its first byte is still answered with a status.
func (f *feed) answer(ctx context.Context, w http.ResponseWriter, kind string, s timeline.Segment, verb string, write func(io.Writer) error) {
	out := &committing{w: w, kind: kind}
	if err := write(out); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "castor could not "+verb+" a segment", "input", f.name, "uri", s.URI, "error", err)
		if !out.started {
			failed(w, err)
		} else {
			// The status is already committed; abort the response so a reader sees truncation and retries.
			panic(http.ErrAbortHandler)
		}
	}
}

// repackaged serves an fMP4 fragment as MPEG-TS, its init section ahead of it.
func (f *feed) repackaged(ctx context.Context, w http.ResponseWriter, s timeline.Segment) {
	carries, err := f.carries(ctx, *s.Map)
	if err == nil && (!carries || (s.Key != timeline.Key{} && !decryptable(s.Key))) {
		// The playlist already names it as MPEG-TS, so a segment that cannot become one is a loss to report.
		err = errors.New("the segment's tracks or encryption cannot be carried as MPEG-TS")
	}
	var init []byte
	if err == nil {
		init, err = f.initBytes(ctx, *s.Map)
	}
	var body io.ReadCloser
	if err == nil {
		body, err = f.read(ctx, s)
	}
	if err != nil {
		slog.WarnContext(ctx, "castor could not serve a segment", "input", f.name, "uri", s.URI, "error", err)
		failed(w, err)
		return
	}
	defer func() { _ = body.Close() }()
	f.answer(ctx, w, media.MPEGTS, s, "repackage", func(out io.Writer) error {
		return f.repackage(ctx, io.MultiReader(bytes.NewReader(init), body), out)
	})
}

// init serves an init section the playlist names, decrypted when the origin encrypted it whole.
func (f *feed) init(w http.ResponseWriter, r *http.Request) {
	if !servable(w, r) {
		return
	}
	m, ok := f.inits.lookup(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err := f.initBytes(r.Context(), m)
	if err != nil {
		failed(w, err)
		return
	}
	_, _ = (&committing{w: w, kind: media.MP4}).Write(b)
}

// key relays a key the reader decrypts with itself.
func (f *feed) key(w http.ResponseWriter, r *http.Request) {
	if !servable(w, r) {
		return
	}
	uri, ok := f.keys.lookup(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err := f.keyBytes(r.Context(), uri)
	if err != nil {
		failed(w, err)
		return
	}
	_, _ = (&committing{w: w, kind: "application/octet-stream"}).Write(b)
}

// servable answers what castor never reads the origin for: a HEAD, and a resume partway into a resource it builds afresh.
func servable(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return false
	}
	if rng := r.Header.Get("Range"); rng != "" && !strings.HasPrefix(rng, "bytes=0-") {
		http.Error(w, "castor serves each resource whole", http.StatusRequestedRangeNotSatisfiable)
		return false
	}
	return true
}

// committing sends its content type with the first byte, so a failure before any can still answer with a status.
type committing struct {
	w       http.ResponseWriter
	kind    string
	started bool
}

func (c *committing) Write(b []byte) (int, error) {
	if !c.started && len(b) > 0 {
		c.w.Header().Set("Content-Type", c.kind)
		c.started = true
	}
	return c.w.Write(b)
}

// failed answers with the origin's own refusal, and a bad gateway for anything the origin never said.
func failed(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	if f, ok := errors.AsType[*timeline.Failure](err); ok && f.Status >= http.StatusBadRequest {
		status = f.Status
	}
	http.Error(w, err.Error(), status)
}
