package follow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// feed is one source's ledger, merged each time its playlist is asked for; castor reads every resource it lists.
type feed struct {
	name      string
	source    timeline.Source
	patience  time.Duration
	repackage Repackage

	// refreshing serialises origin reads, so a slow reload never holds up a segment the reader asks for meanwhile.
	refreshing sync.Mutex

	mu     sync.Mutex
	ledger ledger
	render []byte
	// repackaging is decided once, before the first render, since a playlist cannot take back a MAP it has named.
	repackaging *bool
	carriage    map[timeline.Map]bool

	inits     registry[timeline.Map]
	keys      registry[string]
	initsHeld resources[timeline.Map]
	keysHeld  resources[string]
}

// newFeed follows source under name, giving each origin read patience; repackage, when set, serves fMP4 as MPEG-TS.
func newFeed(name string, source timeline.Source, patience time.Duration, repackage Repackage) *feed {
	return &feed{name: name, source: source, patience: patience, repackage: repackage, carriage: map[timeline.Map]bool{}}
}

// refresh reads the origin's window and merges it.
func (f *feed) refresh(ctx context.Context) error {
	f.refreshing.Lock()
	defer f.refreshing.Unlock()
	f.mu.Lock()
	closed := f.ledger.Closed()
	f.mu.Unlock()
	if closed {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, f.patience)
	defer cancel()
	w, err := f.source.Window(ctx)
	if err != nil {
		return err
	}
	if err := f.decide(ctx, w); err != nil {
		return err
	}
	f.mu.Lock()
	merged := f.ledger.Merge(w)
	f.render = f.ledger.Render(f.view)
	f.mu.Unlock()
	if merged.Restarts > 0 || merged.Gaps > 0 {
		slog.WarnContext(ctx, "the origin's timeline broke; castor marked a seam and carried on",
			"input", f.name, "restarts", merged.Restarts, "gaps", merged.Gaps)
	}
	return nil
}

// startTries is how often a feed's first window is asked for: an origin's passing failure must not cost the link.
const startTries = 3

// start reads the window a feed begins from, again after a passing failure, never after the origin's final no.
func (f *feed) start(ctx context.Context) error {
	var err error
	for try := range startTries {
		if err = f.refresh(ctx); err == nil || refusal(err) != 0 || try == startTries-1 {
			break
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(time.Second << try):
		}
	}
	return err
}

// decide settles whether this feed repackages, from the first init section it meets; undecided, nothing is rendered.
func (f *feed) decide(ctx context.Context, w timeline.Window) error {
	f.mu.Lock()
	decided := f.repackaging != nil
	f.mu.Unlock()
	// A window listing nothing names no MAP yet, so it is no evidence either way.
	if decided || len(w.Segments) == 0 {
		return nil
	}
	repackaging := false
	for _, s := range w.Segments {
		if f.repackage == nil || s.Map == nil {
			continue
		}
		carries, err := f.carries(ctx, *s.Map)
		if err != nil {
			return fmt.Errorf("reading the init section that decides how castor serves %s: %w", f.name, err)
		}
		repackaging = carries
		break
	}
	f.mu.Lock()
	f.repackaging = &repackaging
	f.mu.Unlock()
	return nil
}

// carries reports an init section whose every track MPEG-TS carries, reading it once.
func (f *feed) carries(ctx context.Context, m timeline.Map) (bool, error) {
	f.mu.Lock()
	verdict, known := f.carriage[m]
	f.mu.Unlock()
	if known {
		return verdict, nil
	}
	init, err := f.initBytes(ctx, m)
	if err != nil {
		return false, err
	}
	verdict = tsCarries(init)
	f.mu.Lock()
	f.carriage[m] = verdict
	f.mu.Unlock()
	return verdict, nil
}

// view is a segment as the reader is told to fetch it: from castor, which reads the origin with the live session.
func (f *feed) view(s timeline.Segment, sequence int64) timeline.Segment {
	seen := timeline.Segment{URI: fmt.Sprintf("%s/%d", f.name, sequence), Duration: s.Duration, Seam: s.Seam, Place: s.Place}
	if s.Map != nil && *f.repackaging {
		seen.URI += repackagedExtension
		return seen
	}
	if s.Map != nil {
		seen.Map = &timeline.Map{URI: fmt.Sprintf("%s/init/%d", f.name, f.inits.id(*s.Map))}
	}
	// Castor decrypts what it can; any other key it relays, for the reader to apply.
	if s.Key != (timeline.Key{}) && !decryptable(s.Key) {
		seen.Key = s.Key
		seen.Key.URI = fmt.Sprintf("%s/key/%d", f.name, f.keys.id(s.Key.URI))
	}
	return seen
}

func (f *feed) playlist(w http.ResponseWriter, r *http.Request) {
	err := f.refresh(r.Context())
	f.mu.Lock()
	render := f.render
	f.mu.Unlock()
	switch status := refusal(err); {
	case status != 0:
		http.Error(w, err.Error(), status)
		return
	case err != nil && render == nil:
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	case err != nil:
		// A reload the origin could not answer ends ffmpeg's read, so the last render stands in for it.
		slog.DebugContext(r.Context(), "the origin could not answer a reload; answering from the last render",
			"input", f.name, "error", err)
	}
	w.Header().Set("Content-Type", media.HLS)
	_, _ = w.Write(render)
}

// final is an origin's no, as opposed to a moment it could not answer.
var final = []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone}

// refusal is the status to relay for err, 0 when the origin only failed to answer this once.
func refusal(err error) int {
	if f, ok := errors.AsType[*timeline.Failure](err); ok && slices.Contains(final, f.Status) {
		return f.Status
	}
	return 0
}
