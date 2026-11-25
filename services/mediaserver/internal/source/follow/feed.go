package follow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// feed is one source's ledger, merged each time its playlist is asked for; castor reads every resource it lists.
type feed struct {
	// unwrapped combines the segment formats decoded during this reading.
	unwrapped atomic.Uint32
	name      string
	source    timeline.Source
	decoder   timeline.Decoder
	repackage Repackage

	// refreshing serialises origin reads, so a slow reload never holds up a segment the reader asks for meanwhile.
	refreshing sync.Mutex

	mu      sync.Mutex
	ledger  ledger
	current playlistResult
	// repackaging is decided once, before the first render, since a playlist cannot take back a MAP it has named.
	repackaging *bool
	carriage    map[timeline.Map]bool

	inits     registry[timeline.Map]
	keys      registry[string]
	initsHeld resources[timeline.Map]
	keysHeld  resources[string]

	// life bounds every read ahead of the reader; reached is the furthest segment it asked for.
	work     sync.WaitGroup
	life     context.Context
	reached  int64
	slots    map[int64]*slot
	inFlight int
	// took is how long the last few segment reads took, which sets when a slow one is asked for again.
	took ring
}

// playlistResult is the published response. A refusal replaces the playlist;
// only a successful reload can publish media again.
type playlistResult struct {
	body []byte
	err  error
}

// newFeed follows source under name; repackage, when set, serves fMP4 as MPEG-TS.
func newFeed(ctx context.Context, name string, reading timeline.Reading, repackage Repackage) *feed {
	return &feed{name: name, source: reading.Source, decoder: reading.Decoder, repackage: repackage, carriage: map[timeline.Map]bool{}, slots: map[int64]*slot{}, reached: -1, life: ctx}
}

// refresh reads and merges one window, returning its published result before another reload can replace it.
func (f *feed) refresh(ctx context.Context) (playlistResult, error) {
	f.refreshing.Lock()
	defer f.refreshing.Unlock()
	f.mu.Lock()
	closed := f.ledger.Closed()
	f.mu.Unlock()
	if closed {
		return f.result(), nil
	}
	ctx, cancel := context.WithTimeout(ctx, patience)
	defer cancel()
	w, err := f.source.Window(ctx)
	if err == nil {
		err = f.decide(ctx, w)
	}
	if err != nil {
		if refusal(err) != 0 {
			f.mu.Lock()
			f.current = playlistResult{err: err}
			f.mu.Unlock()
		}
		return f.result(), err
	}
	f.mu.Lock()
	merged := f.ledger.Merge(w)
	f.current = playlistResult{body: f.ledger.Render(f.view)}
	f.pruneResources()
	f.mu.Unlock()
	if merged.Restarts > 0 || merged.Gaps > 0 {
		slog.WarnContext(ctx, "the origin's timeline broke; castor marked a seam and carried on",
			"input", f.name, "restarts", merged.Restarts, "gaps", merged.Gaps)
	}
	return f.result(), nil
}

// startTries is how often a feed's first window is asked for: an origin's passing failure must not cost the link.
const startTries = 3

// start reads the window a feed begins from, again after a passing failure, never after the origin's final no; ctx bounds its reads ahead.
func (f *feed) start() error {
	ctx := f.life
	var err error
	for try := range startTries {
		if _, err = f.refresh(ctx); err == nil || refusal(err) != 0 || try == startTries-1 {
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
	if s.Key != (timeline.Key{}) && !f.decoder.ConsumesKey(s.Key) {
		seen.Key = s.Key
		seen.Key.URI = fmt.Sprintf("%s/key/%d", f.name, f.keys.id(s.Key.URI))
	}
	return seen
}

func (f *feed) playlist(w http.ResponseWriter, r *http.Request) {
	result, err := f.refresh(r.Context())
	switch {
	case result.err != nil:
		http.Error(w, result.err.Error(), refusal(result.err))
		return
	case err != nil && result.body == nil:
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	case err != nil:
		slog.DebugContext(r.Context(), "the origin could not answer a reload; answering from the last render",
			"input", f.name, "error", err)
	}
	w.Header().Set("Content-Type", media.HLS)
	w.Header().Set("Content-Length", strconv.Itoa(len(result.body)))
	_, _ = w.Write(result.body)
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

// pruneResources retains names only while their segments can be served. Readers
// of expired playlists receive a missing resource, never another segment's key.
// f.mu must be held.
func (f *feed) pruneResources() {
	inits := make(map[timeline.Map]bool)
	keys := make(map[string]bool)
	for _, e := range f.ledger.entries {
		if e.Map != nil {
			inits[*e.Map] = true
		}
		if e.Key.URI != "" {
			keys[e.Key.URI] = true
		}
	}
	f.inits.retain(inits)
	f.keys.retain(keys)
	for m := range f.carriage {
		if !inits[m] {
			delete(f.carriage, m)
		}
	}
}

// wait runs after callers and request handlers have stopped launching work.
func (f *feed) wait() {
	f.work.Wait()
	f.initsHeld.reads.Wait()
	f.keysHeld.reads.Wait()
}

// result is shared by HTTP serving and the final read outcome.
func (f *feed) result() playlistResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}
