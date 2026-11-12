package airplay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

// maxPosition is the most a cast's position can be, which also keeps a duration from overflowing.
const maxPosition = 365 * 24 * time.Hour

// verdictWithin bounds how long Play waits for the receiver to play the media. A receiver still loading by
// then has refused it: an LG answered a file it could not fetch only by loading for ever.
var verdictWithin = 30 * time.Second

// ending is one Play's end: done closes once the receiver says how that playback ended.
type ending struct {
	once sync.Once
	done chan struct{}
	err  error
}

func newEnding() *ending { return &ending{done: make(chan struct{})} }

func (e *ending) finish(err error) {
	e.once.Do(func() {
		e.err = err
		close(e.done)
	})
}

func (e *ending) over() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// playhead follows what the receiver reports of the one item castor handed it.
type playhead struct {
	mu      sync.Mutex
	started bool
	placed  bool
	head    device.Playhead
	end     *ending

	// decided closes on the receiver's verdict: nil once it plays the item, else why it will not.
	decided    chan struct{}
	decision   error
	decideOnce sync.Once
}

func newPlayhead() *playhead { return &playhead{end: newEnding(), decided: make(chan struct{})} }

func (p *playhead) decide(err error) {
	p.decideOnce.Do(func() {
		p.decision = err
		close(p.decided)
	})
}

// over ends the item; ending before the receiver played it is its refusal.
func (p *playhead) over(err error) {
	p.end.finish(err)
	p.decide(err)
}

// errRefused is a receiver that would not play what castor handed it, as opposed to a link that failed.
var errRefused = errors.New("the receiver refused the media")

// verdict waits for the receiver to play the item, or for its refusal.
func (p *playhead) verdict(ctx context.Context) error {
	timer := time.NewTimer(verdictWithin)
	defer timer.Stop()
	select {
	case <-p.decided:
	case <-timer.C:
		p.over(fmt.Errorf("the receiver did not begin playing within %s", verdictWithin))
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	<-p.decided
	if p.decision == nil {
		return nil
	}
	if _, gone := errors.AsType[*device.Gone](p.decision); gone {
		return p.decision
	}
	return fmt.Errorf("%w: %w", errRefused, p.decision)
}

func (p *playhead) position() (device.Playhead, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.placed {
		return device.Playhead{}, device.ErrNoPosition
	}
	return p.head, nil
}

// played ends the item once it has played, or fails it as refused where it never began.
func (p *playhead) played(refused string) {
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if started {
		p.over(nil)
		return
	}
	p.over(errors.New(refused))
}

// mediaTime is a CMTime as AirPlay sends it, a dictionary of value, timescale, flags and epoch.
func mediaTime(v any) (time.Duration, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return 0, false
	}
	value, okValue := number(m["value"])
	scale, okScale := number(m["timescale"])
	flags, _ := number(m["flags"])
	// Valid (1) and none of positive infinity (4), negative infinity (8) or indefinite (16).
	f := int64(flags)
	if !okValue || !okScale || scale <= 0 || f&1 == 0 || f&(4|8|16) != 0 {
		return 0, false
	}
	return seconds(value / scale)
}

// seconds is s as a position, false for none a cast can be at.
func seconds(s float64) (time.Duration, bool) {
	if math.IsNaN(s) || s < 0 || s > maxPosition.Seconds() {
		return 0, false
	}
	return time.Duration(s * float64(time.Second)), true
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case uint64:
		return float64(n), true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// window is the first of a receiver's time ranges, each a start and a duration read by at.
func window(v any, at func(any) (time.Duration, bool)) *device.SeekWindow {
	ranges, ok := v.([]any)
	if !ok || len(ranges) == 0 {
		return nil
	}
	r, ok := ranges[0].(map[string]any)
	if !ok {
		return nil
	}
	start, okStart := at(r["start"])
	length, okLength := at(r["duration"])
	if !okStart || !okLength {
		return nil
	}
	return &device.SeekWindow{Start: start, End: start + length}
}

// v2Report updates p from a video v2 playback report, an info dictionary carrying rate, state and times.
func (p *playhead) v2Report(info map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, _ := info["playbackState"].(string)
	if state == "playing" || state == "paused" {
		p.started = true
		p.decide(nil)
	}
	// A stopped receiver reports zero, which is no position of the item (as a DLNA renderer does once stopped).
	if !p.started || state == "stopped" || p.end.over() {
		return
	}
	if at, ok := mediaTime(info["position"]); ok {
		p.head.At, p.placed = at, true
	}
	if rate, ok := number(info["rate"]); ok {
		p.head.Paused = rate == 0 || state == "paused"
	}
	if w := window(info["seekableTimeRanges"], mediaTime); w != nil {
		p.head.Seekable = w
	}
}

// v2Command follows one command the receiver sent castor about the queued item.
func (p *playhead) v2Command(item string, c map[string]any, answered func(uint64)) {
	kind, _ := c["type"].(string)
	name, _ := c["name"].(string)
	if !forItem(c, item) {
		return
	}
	switch kind {
	case "playbackState":
		if params, ok := c["params"].(map[string]any); ok {
			p.v2Report(params)
		} else if name == "playing" || name == "paused" {
			p.mu.Lock()
			p.started = true
			p.mu.Unlock()
			p.decide(nil)
		}
		if name == "stopped" {
			p.played("the receiver stopped before playing the media")
		}
	case "playbackInfo":
		if info, ok := c["info"].(map[string]any); ok {
			p.v2Report(info)
		}
		if id, ok := number(c["messageID"]); ok {
			answered(uint64(id))
		}
	case "notification":
		switch name {
		case "itemPlayedToEnd":
			p.over(nil)
		case "itemFailedToPlayToEnd":
			p.over(fmt.Errorf("the receiver stopped playing the media: %v", c["params"]))
		case "itemRemoved":
			p.played("the receiver dropped the media before playing it")
		}
	case "error":
		code := "unknown"
		if e, ok := c["error"].(map[string]any); ok {
			code = fmt.Sprint(e["code"])
		}
		p.over(fmt.Errorf("the receiver could not play the media (error %s)", code))
	case "unhandledURL":
		// The receiver asks the sender to fetch what it could not reach itself, which castor does not do.
		if key, _ := nested(c, "request", "FCUP_Response_IsContentKeyRequest").(bool); key {
			return
		}
		url := nested(c, "request", "FCUP_Response_URL")
		p.mu.Lock()
		started := p.started
		p.mu.Unlock()
		// Before the item plays, that is its refusal. Once it plays, a live playlist reload it missed is not its end:
		// the receiver asks again, or gives up with an error of its own, and only that ends the cast.
		if started {
			slog.Warn("the receiver could not fetch part of the playing media itself", "url", url,
				"request", nested(c, "request", "FCUP_Response_RequestID"), "headers", nested(c, "request", "FCUP_Response_Headers"))
			return
		}
		p.over(fmt.Errorf("the receiver could not fetch %v itself", url))
	}
}

// forItem is whether a command concerns item, a command naming no item concerning the only one castor queues.
func forItem(c map[string]any, item string) bool {
	for _, key := range []string{"item", "itemCurrent", "itemBefore"} {
		if id, ok := nested(c, key, "uuid").(string); ok {
			return id == item
		}
	}
	if params, ok := c["params"].(map[string]any); ok {
		if id, ok := nested(params, "item", "uuid").(string); ok {
			return id == item
		}
	}
	if id, ok := c["uuid"].(string); ok {
		return id == item
	}
	return true
}

func nested(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[k]
	}
	return v
}
