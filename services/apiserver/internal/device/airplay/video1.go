package airplay

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"howett.net/plist"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

const (
	// playRetries is how often castor hands an Apple TV the URL again after an internal error, as pyatv does.
	playRetries = 3
	retryPause  = time.Second

	infoInterval = time.Second
)

// videoV1 is a URL posted to an Apple TV's /play, followed through /playback-info.
type videoV1 struct {
	l *link
	// s is the AirPlay 2 session an AirPlay 2 Apple TV plays within, nil for one speaking AirPlay 1.
	s    *ap2
	head *playhead

	stopPoll chan struct{}
	stopOnce sync.Once
}

// playV1 posts the URL the way pyatv does for the receiver's AirPlay version, retrying a receiver's internal error.
func playV1(ctx context.Context, l *link, secret []byte, playback device.Playback) (*videoV1, error) {
	v := &videoV1{l: l, head: newPlayhead(), stopPoll: make(chan struct{})}
	if secret != nil {
		s, err := startAP2(ctx, l, secret, func(map[string]any) {})
		if err != nil {
			return nil, err
		}
		v.s = s
	}
	if err := v.post(ctx, playback); err != nil {
		v.close()
		return nil, err
	}
	go v.poll()
	if v.s != nil {
		go func() {
			select {
			case <-v.s.gone():
				v.head.over(&device.Gone{Device: l.host(), Observed: "the Apple TV closed the session while the cast was playing"})
			case <-v.head.end.done:
			}
		}()
	}
	if err := v.head.verdict(ctx); err != nil {
		v.close()
		return nil, err
	}
	return v, nil
}

func (v *videoV1) post(ctx context.Context, playback device.Playback) error {
	session := newUUID()
	var body map[string]any
	header := map[string]string{"X-Apple-Session-ID": session}
	if v.s != nil {
		header["X-Apple-ProtocolVersion"] = "1"
		header["X-Apple-Stream-ID"] = "1"
		body = map[string]any{
			"Content-Location": playback.URL.String(), "Start-Position-Seconds": 0.0, "uuid": session,
			"streamType": 1, "mediaType": "file", "playbackRestrictions": 0, "referenceRestrictions": 3,
			"clientBundleID": bundleID, "clientProcName": bundleID, "volume": 1.0, "rate": 1.0,
		}
	} else {
		header["User-Agent"] = "MediaControl/1.0"
		body = map[string]any{"Content-Location": playback.URL.String(), "Start-Position": 0.0, "X-Apple-Session-ID": session}
	}
	for attempt := 1; ; attempt++ {
		resp, err := v.l.exchangePlist(ctx, "POST", "/play", protoHTTP, header, body)
		if err != nil {
			return fmt.Errorf("handing the Apple TV the media: %w", err)
		}
		if resp.code == 500 && attempt < playRetries {
			select {
			case <-time.After(retryPause):
				continue
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		if err := ok(resp, "the media"); err != nil {
			return err
		}
		break
	}
	if v.s == nil {
		return nil
	}
	// pyatv sends these after /play; the rate is what starts playback, an Apple TV holding paused without it.
	for _, set := range []struct {
		uri   string
		value any
	}{
		{"/setProperty?isInterestedInDateRange", true},
		{"/setProperty?actionAtItemEnd", 0},
	} {
		if _, err := v.l.exchangePlist(ctx, "PUT", set.uri, protoRTSP, nil, map[string]any{"value": set.value}); err != nil {
			return err
		}
	}
	return v.rate(ctx, 1)
}

// playbackInfo is what /playback-info reports, every time in seconds.
type playbackInfo struct {
	Duration    *float64     `plist:"duration"`
	Position    *float64     `plist:"position"`
	Rate        *float64     `plist:"rate"`
	ReadyToPlay bool         `plist:"readyToPlay"`
	Seekable    []timeRange  `plist:"seekableTimeRanges"`
	Error       *playerError `plist:"error"`
}

type timeRange struct {
	Start    float64 `plist:"start"`
	Duration float64 `plist:"duration"`
}

type playerError struct {
	Code   int64  `plist:"code"`
	Domain string `plist:"domain"`
}

// poll reads /playback-info every second: a duration means media is playing, and its disappearing is the end.
func (v *videoV1) poll() {
	ticker := time.NewTicker(infoInterval)
	defer ticker.Stop()
	for {
		select {
		case <-v.stopPoll:
			return
		case <-v.head.end.done:
			return
		case <-ticker.C:
		}
		info, err := v.info(context.Background())
		if err != nil {
			select {
			case <-v.stopPoll:
			default:
				v.head.over(&device.Gone{Device: v.l.host(), Observed: "the Apple TV stopped answering while the cast was playing", Err: err})
			}
			return
		}
		v.observe(info)
	}
}

func (v *videoV1) info(ctx context.Context) (playbackInfo, error) {
	resp, err := v.l.exchange(ctx, request{method: "GET", uri: "/playback-info", proto: protoHTTP})
	if err != nil {
		return playbackInfo{}, err
	}
	if err := ok(resp, "reporting playback"); err != nil {
		return playbackInfo{}, err
	}
	var info playbackInfo
	if len(resp.body) > 0 {
		if _, err := plist.Unmarshal(resp.body, &info); err != nil {
			return playbackInfo{}, fmt.Errorf("the Apple TV reported playback unreadably: %w", err)
		}
	}
	return info, nil
}

func (v *videoV1) observe(info playbackInfo) {
	if info.Error != nil {
		v.head.over(fmt.Errorf("the Apple TV could not play the media (error %d in %q)", info.Error.Code, info.Error.Domain))
		return
	}
	p := v.head
	p.mu.Lock()
	defer p.mu.Unlock()
	if info.Duration == nil {
		if p.started {
			p.over(nil)
		}
		return
	}
	p.started = true
	p.decide(nil)
	if info.Position != nil {
		if at, ok := seconds(*info.Position); ok {
			p.head.At, p.placed = at, true
		}
	}
	if info.Rate != nil {
		p.head.Paused = *info.Rate == 0
	}
	if len(info.Seekable) > 0 {
		r := info.Seekable[0]
		start, okStart := seconds(r.Start)
		length, okLength := seconds(r.Duration)
		if okStart && okLength {
			p.head.Seekable = &device.SeekWindow{Start: start, End: start + length}
		}
	}
}

func (v *videoV1) position(context.Context) (device.Playhead, error) { return v.head.position() }

func (v *videoV1) rate(ctx context.Context, rate float64) error {
	proto := protoHTTP
	if v.s != nil {
		proto = protoRTSP
	}
	resp, err := v.l.exchange(ctx, request{method: "POST", uri: fmt.Sprintf("/rate?value=%f", rate), proto: proto})
	if err != nil {
		return err
	}
	return ok(resp, "the playback rate")
}

func (v *videoV1) setRate(ctx context.Context, rate float64) error { return v.rate(ctx, rate) }

func (v *videoV1) ending() *ending { return v.head.end }

// close stops the Apple TV's player and leaves the session.
func (v *videoV1) close() {
	v.stopOnce.Do(func() { close(v.stopPoll) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = v.l.exchange(ctx, request{method: "POST", uri: "/stop", proto: protoHTTP})
	if v.s != nil {
		v.s.close()
	}
	_ = v.l.Close()
}

var errNoVideo = errors.New("the receiver plays no video over AirPlay")
