// Package splice indexes a growing buffer's keyframes and reads it from one, so a device's feed changes unnoticed.
package splice

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// ErrNoCut is a stream that ended before it reached a cut.
var ErrNoCut = errors.New("the stream ended before a keyframe to cut at")

// Cut is a place the stream can be cut: a keyframe, with the tables a reader starting there needs.
type Cut struct {
	// Offset is where the keyframe starts in the buffer.
	Offset int64
	// PTS is the keyframe's presentation time.
	PTS time.Duration
	// Tables are what the stream said of its streams at Offset, which a reader starting there is given first.
	Tables []byte
}

// Index lists the cuts of a stream that is being written, in order.
type Index struct {
	mu     sync.Mutex
	more   chan struct{}
	closed bool
	cuts   []Cut
	// latest is the time of the last picture seen, whether a keyframe or not.
	latest time.Duration
	seen   bool

	// sounds lists where each audio frame opens, and when.
	sounds []mark
	scan   Scanner
}

func NewIndex(f Framing) *Index {
	return &Index{more: make(chan struct{}), scan: f.Scanner()}
}

// Write scans p, which must be written to the buffer before it is indexed: a cut is only listed once its bytes are readable.
func (x *Index) Write(p []byte) (int, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	listed := false
	x.scan.Scan(p, func(e Event) {
		switch e.Kind {
		case Sound:
			x.sounds = append(x.sounds, mark{offset: e.Offset, pts: e.PTS})
			return
		case Keyframe:
			x.cuts = append(x.cuts, Cut{Offset: e.Offset, PTS: e.PTS, Tables: e.Tables})
			listed = true
		}
		x.latest, x.seen = max(x.latest, e.PTS), true
	})
	if listed {
		close(x.more)
		x.more = make(chan struct{})
	}
	return len(p), nil
}

// mark is where a frame opens in the stream, and its time.
type mark struct {
	offset int64
	pts    time.Duration
}

// SoundAfter is the time of the first audio frame that opens at or past offset, if the stream has reached one.
func (x *Index) SoundAfter(offset int64) (time.Duration, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	i, _ := slices.BinarySearchFunc(x.sounds, offset, func(m mark, o int64) int { return cmp.Compare(m.offset, o) })
	if i == len(x.sounds) {
		return 0, false
	}
	return x.sounds[i].pts, true
}

// Extent is the media the stream holds, from its first keyframe to its last picture.
func (x *Index) Extent() time.Duration {
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.cuts) == 0 || !x.seen {
		return 0
	}
	return max(0, x.latest-x.cuts[0].PTS)
}

// At is the time of the last frame that opens before offset, which a reader that far in has passed.
func (x *Index) At(offset int64) (time.Duration, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	var at time.Duration
	known := false
	if i, _ := slices.BinarySearchFunc(x.sounds, offset, func(m mark, o int64) int { return cmp.Compare(m.offset, o) }); i > 0 {
		at, known = x.sounds[i-1].pts, true
	}
	if i, _ := slices.BinarySearchFunc(x.cuts, offset, func(c Cut, o int64) int { return cmp.Compare(c.Offset, o) }); i > 0 {
		at, known = max(at, x.cuts[i-1].PTS), true
	}
	return at, known
}

// First is the time of the first cut, which is where the stream's picture begins.
func (x *Index) First() (time.Duration, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.cuts) == 0 {
		return 0, false
	}
	return x.cuts[0].PTS, true
}

// After is the first cut at or past offset, if the stream has reached one.
func (x *Index) After(offset int64) (Cut, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.after(offset)
}

func (x *Index) after(offset int64) (Cut, bool) {
	i, _ := slices.BinarySearchFunc(x.cuts, offset, func(c Cut, o int64) int { return cmp.Compare(c.Offset, o) })
	if i == len(x.cuts) {
		return Cut{}, false
	}
	return x.cuts[i], true
}

// Wait is the first cut at or past offset, once the stream has reached one; it gives up when ctx ends or the stream does.
func (x *Index) Wait(ctx context.Context, offset int64) (Cut, error) {
	for {
		x.mu.Lock()
		cut, ok := x.after(offset)
		closed, more := x.closed, x.more
		x.mu.Unlock()
		switch {
		case ok:
			return cut, nil
		case closed:
			return Cut{}, ErrNoCut
		}
		select {
		case <-more:
		case <-ctx.Done():
			return Cut{}, context.Cause(ctx)
		}
	}
}

// Close says the stream has ended, so no further cut will be listed.
func (x *Index) Close() {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.closed {
		x.closed = true
		close(x.more)
		x.more = make(chan struct{})
	}
}

// ErrNoMatch is a stream that passed a time without a cut there.
var ErrNoMatch = errors.New("the stream has no keyframe at that time")

// Matching waits for the cut at pts within tolerance, giving up once the stream passed pts without one.
func (x *Index) Matching(ctx context.Context, pts, tolerance time.Duration) (Cut, error) {
	for {
		x.mu.Lock()
		i := slices.IndexFunc(x.cuts, func(c Cut) bool { return c.PTS >= pts-tolerance })
		var cut Cut
		if i >= 0 {
			cut = x.cuts[i]
		}
		closed, more := x.closed, x.more
		x.mu.Unlock()
		switch {
		case i >= 0 && cut.PTS <= pts+tolerance:
			return cut, nil
		case i >= 0:
			return Cut{}, ErrNoMatch
		case closed:
			return Cut{}, ErrNoCut
		}
		select {
		case <-more:
		case <-ctx.Done():
			return Cut{}, context.Cause(ctx)
		}
	}
}
