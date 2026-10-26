package receiver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// patience is how long a device plays on an empty buffer before it gives up, as a TV reports STOPPED within seconds.
const patience = 5 * time.Second

// errStarved ends a playback whose buffer ran dry while the device was playing.
var errStarved = errors.New("the device played out its buffer")

// clock is the device's playback clock: media timestamps taken in against wall time spent playing.
type clock struct {
	mu sync.Mutex
	// from is where playback last began; first and latest are the presentation times seen since, started when the first landed.
	from, first, latest time.Duration
	started             time.Time
	// heldSince is when playback was last paused, zero while playing; held is every pause before it, heldBefore those before from.
	heldSince        time.Time
	held, heldBefore time.Duration
	// holds counts every time playback was paused.
	holds int
}

// restart begins playback again at position, as a device does when its viewer jumps there.
func (c *clock) restart(position time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.from, c.first, c.latest, c.started, c.heldBefore = position, 0, 0, time.Time{}, c.held
}

// reach waits until playback is at position, false if ctx ends first.
func (c *clock) reach(ctx context.Context, position time.Duration) bool {
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for c.played(time.Now()) < position {
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
	return true
}

// taken is the position playback reaches once it has played everything taken in.
func (c *clock) taken() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.from + c.latest - c.first
}

// playing is the wall time spent playing since the first presentation time landed.
func (c *clock) playing(now time.Time) time.Duration {
	held := c.held - c.heldBefore
	if !c.heldSince.IsZero() {
		held += now.Sub(c.heldSince)
	}
	return now.Sub(c.started) - held
}

// hold pauses or resumes the clock.
func (c *clock) hold(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case on && c.heldSince.IsZero():
		c.heldSince = time.Now()
		c.holds++
	case !on && !c.heldSince.IsZero():
		c.held += time.Since(c.heldSince)
		c.heldSince = time.Time{}
	}
}

// paused is how often and how long playback was held.
func (c *clock) paused() (int, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	held := c.held
	if !c.heldSince.IsZero() {
		held += time.Since(c.heldSince)
	}
	return c.holds, held
}

// played is the position the device has played to by now: where it began, plus wall time playing, never past what it was given.
func (c *clock) played(now time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started.IsZero() {
		return c.from
	}
	return c.from + max(0, min(c.playing(now), c.latest-c.first))
}

func (c *clock) holding() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.heldSince.IsZero()
}

// buffered is the media taken in and not yet played, and whether anything has been taken in at all.
func (c *clock) buffered(now time.Time) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started.IsZero() {
		return 0, false
	}
	return (c.latest - c.first) - c.playing(now), true
}

func (c *clock) observe(pts time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started.IsZero() {
		c.started, c.first = time.Now(), pts
	}
	c.latest = max(c.latest, pts)
}

// starve ends ctx with errStarved once the buffer has been empty for patience while the device plays.
func (c *clock) starve(ctx context.Context, cancel context.CancelCauseFunc) {
	tick := time.NewTicker(patience / 10)
	defer tick.Stop()
	var empty time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			left, ok := c.buffered(now)
			switch {
			case !ok || left > 0 || c.holding():
				empty = time.Time{}
			case empty.IsZero():
				empty = now
			case now.Sub(empty) > patience:
				cancel(errStarved)
				return
			}
		}
	}
}

// deviceBuffer is how far ahead of playback a device playing in real time reads.
const deviceBuffer = 5 * time.Second

// ahead holds reads while the device has a full buffer it has not played yet.
type ahead struct {
	r     io.Reader
	ctx   context.Context
	clock *clock
}

func (a ahead) Read(p []byte) (int, error) {
	if !a.clock.reach(a.ctx, a.clock.taken()-deviceBuffer) {
		return 0, context.Cause(a.ctx)
	}
	return a.r.Read(p)
}

// tsPacket is the size of one MPEG-TS packet, and tsSync the byte every one opens with.
const (
	tsPacket = 188
	tsSync   = 0x47
)

// timed reads an MPEG-TS stream through, feeding every presentation timestamp to the clock; another container passes untimed.
type timed struct {
	r     io.Reader
	clock *clock
	rest  []byte
}

func (t *timed) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	t.rest = append(t.rest, p[:n]...)
	for len(t.rest) >= tsPacket {
		if t.rest[0] != tsSync {
			if i := bytes.IndexByte(t.rest[1:], tsSync); i >= 0 {
				t.rest = t.rest[i+1:]
				continue
			}
			t.rest = t.rest[:0]
			break
		}
		if pts, ok := presentationTime(t.rest[:tsPacket]); ok {
			t.clock.observe(pts)
		}
		t.rest = t.rest[tsPacket:]
	}
	return n, err
}

// presentationTime reads the PTS of the PES packet a transport packet opens, if it opens one with a PTS.
func presentationTime(pkt []byte) (time.Duration, bool) {
	if pkt[1]&0x40 == 0 {
		return 0, false
	}
	payload := 4
	if pkt[3]&0x20 != 0 {
		payload += 1 + int(pkt[4])
	}
	// A PES header with a PTS runs 14 bytes from its start code.
	if payload+14 > len(pkt) || pkt[payload] != 0 || pkt[payload+1] != 0 || pkt[payload+2] != 1 {
		return 0, false
	}
	h := pkt[payload:]
	if h[6]&0xc0 != 0x80 || h[7]&0x80 == 0 {
		return 0, false
	}
	ticks := int64(h[9]>>1&7)<<30 | int64(h[10])<<22 | int64(h[11]>>1)<<15 | int64(h[12])<<7 | int64(h[13]>>1)
	return time.Duration(ticks) * time.Second / 90000, true
}
