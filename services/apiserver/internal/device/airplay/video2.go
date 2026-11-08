package airplay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

const (
	feedbackInterval = 2 * time.Second

	// The stream type, control type and client type macOS sets up to queue video on a third-party receiver.
	remoteControlStream = 130
	videoControl        = 1
	videoClientType     = "A6B27562-B43A-4F2D-B75F-82391E250194"

	bundleID = "io.github.stupside.castor"
)

// ap2 is an AirPlay 2 session on a sealed link: the event channel, the timing server and the feedback castor keeps sending.
type ap2 struct {
	l      *link
	events *eventChannel
	timing *timingServer
	stop   chan struct{}
	// lost closes once the receiver stops answering feedback.
	lost     chan struct{}
	stopOnce sync.Once
}

// startAP2 sets the session up the way pyatv and macOS do, handing every report the receiver sends to handle.
func startAP2(ctx context.Context, l *link, secret []byte, handle func(map[string]any)) (*ap2, error) {
	timing, err := listenTiming(l.localIP())
	if err != nil {
		return nil, err
	}
	s := &ap2{l: l, timing: timing, stop: make(chan struct{}), lost: make(chan struct{})}
	if err := s.setup(ctx, secret, handle); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (s *ap2) setup(ctx context.Context, secret []byte, handle func(map[string]any)) error {
	resp, err := s.l.exchangePlist(ctx, "SETUP", "", protoRTSP, nil, map[string]any{
		"deviceID":                 senderID,
		"macAddress":               senderID,
		"sessionUUID":              newUUID(),
		"timingPort":               s.timing.port(),
		"timingProtocol":           "NTP",
		"isMultiSelectAirPlay":     true,
		"groupContainsGroupLeader": false,
		"name":                     "Castor",
		"model":                    "Castor",
		"sourceVersion":            "550.10",
		"senderSupportsRelay":      false,
		"statsCollectionEnabled":   false,
	})
	if err != nil {
		return fmt.Errorf("setting up the AirPlay session: %w", err)
	}
	if err := ok(resp, "the AirPlay session"); err != nil {
		return err
	}
	var setup struct {
		EventPort int `plist:"eventPort"`
	}
	if err := resp.decode(&setup); err != nil {
		return err
	}
	if setup.EventPort <= 0 {
		return errors.New("the receiver set the session up without an event port")
	}
	if s.events, err = openEvents(ctx, s.l.host(), setup.EventPort, secret, handle); err != nil {
		return err
	}
	go s.feedback()
	resp, err = s.l.exchange(ctx, request{method: "RECORD"})
	if err != nil {
		return fmt.Errorf("starting the AirPlay session: %w", err)
	}
	return ok(resp, "starting the session")
}

// senderID is the device identity castor's sessions announce; the receiver trusts the pairing, not this.
const senderID = "AA:BB:CC:DD:EE:FF"

// feedback keeps the session alive; a receiver that stops answering it is gone.
func (s *ap2) feedback() {
	ticker := time.NewTicker(feedbackInterval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
		}
		resp, err := s.l.exchange(context.Background(), request{method: "POST", uri: "/feedback"})
		if err == nil && resp.code < 500 {
			failures = 0
			continue
		}
		if failures++; failures == 3 {
			close(s.lost)
			return
		}
	}
}

// gone closes once the receiver closed the event channel or stopped answering.
func (s *ap2) gone() <-chan struct{} {
	out := make(chan struct{})
	go func() {
		select {
		case <-s.events.gone:
		case <-s.lost:
		case <-s.stop:
			return
		}
		// Closing the session closes the channel too, which is no receiver going away.
		select {
		case <-s.stop:
			return
		default:
		}
		close(out)
	}()
	return out
}

func (s *ap2) close() {
	s.stopOnce.Do(func() { close(s.stop) })
	if s.events != nil {
		_ = s.events.Close()
	}
	_ = s.timing.Close()
}

// videoV2 is one item queued on a third-party receiver's remote control stream.
type videoV2 struct {
	s      *ap2
	stream uint64
	item   string
	head   *playhead

	mu      sync.Mutex
	asked   uint64
	waiting map[uint64]chan struct{}
}

func playV2(ctx context.Context, l *link, secret []byte, receiverID string, playback device.Playback) (*videoV2, error) {
	v := &videoV2{item: newUUID(), head: newPlayhead(), waiting: map[uint64]chan struct{}{}}
	s, err := startAP2(ctx, l, secret, func(c map[string]any) { v.head.v2Command(v.item, c, v.answered) })
	if err != nil {
		return nil, err
	}
	v.s = s
	if err := v.queue(ctx, receiverID, playback); err != nil {
		s.close()
		return nil, err
	}
	go func() {
		select {
		case <-s.gone():
			v.head.over(&device.Gone{Device: l.host(), Observed: "the AirPlay receiver closed the session while the cast was playing"})
		case <-v.head.end.done:
		}
	}()
	// A refusal returned from Play is what lets the media server serve what it handed over instead.
	if err := v.head.verdict(ctx); err != nil {
		v.close()
		return nil, err
	}
	return v, nil
}

func (v *videoV2) queue(ctx context.Context, receiverID string, playback device.Playback) error {
	resp, err := v.s.l.exchangePlist(ctx, "SETUP", "", protoRTSP, nil, map[string]any{"streams": []any{map[string]any{
		"type":           remoteControlStream,
		"controlType":    videoControl,
		"channelID":      strings.ToUpper(receiverID) + "-RCS-2",
		"clientUUID":     newUUID(),
		"clientTypeUUID": videoClientType,
	}}})
	if err != nil {
		return fmt.Errorf("setting up the video stream: %w", err)
	}
	if err := ok(resp, "a video stream"); err != nil {
		return err
	}
	var setup struct {
		Streams []struct {
			StreamID uint64 `plist:"streamID"`
		} `plist:"streams"`
	}
	if err := resp.decode(&setup); err != nil {
		return err
	}
	if len(setup.Streams) == 0 {
		return errors.New("the receiver set up no video stream")
	}
	v.stream = setup.Streams[0].StreamID
	for _, command := range []map[string]any{
		{"type": "insertPlayQueueItem", "item": queueItem(v.item, playback)},
		// Hold the last picture at the end, as iOS and macOS do, so the end is reported rather than skipped past.
		{"type": "setProperty", "property": "actionAtItemEnd", "value": 1},
		{"type": "setRate", "rate": 1.0},
	} {
		if err := v.command(ctx, command); err != nil {
			return err
		}
	}
	return nil
}

// invalidTime is the CMTime macOS sends where a time does not apply.
var invalidTime = map[string]any{"flags": 0, "value": 0, "epoch": 0, "timescale": 0}

// queueItem is the item insertPlayQueueItem takes, as macOS sends it: HLS as streaming, which a live playlist needs, the rest as a file.
func queueItem(id string, playback device.Playback) map[string]any {
	item := map[string]any{
		"uuid":                             id,
		"Content-Location":                 playback.URL.String(),
		"playbackRestrictions":             0,
		"referenceRestrictions":            3,
		"supportsIntegratedTimeline":       false,
		"allowedCommonMediaClientDataKeys": 0,
		"snapTimeToPausePlayback":          false,
		"clientBundleID":                   bundleID,
	}
	if playback.Container() == mediav1.Container_CONTAINER_HLS {
		item["mediaType"] = "streaming"
		item["timeToPausePlayback"] = invalidTime
		item["timeToPauseBuffering"] = invalidTime
	} else {
		item["mediaType"] = "file"
		item["Start-Position"] = map[string]any{"flags": 1, "value": 0, "epoch": 0, "timescale": 1_000_000_000}
	}
	return item
}

func (v *videoV2) command(ctx context.Context, command map[string]any) error {
	body, err := encodeCommand(command)
	if err != nil {
		return err
	}
	resp, err := v.s.l.exchange(ctx, request{
		method: "POST", uri: "/command", contentType: contentBplist, body: body,
		header: map[string]string{"X-Apple-StreamID": fmt.Sprint(v.stream)},
	})
	if err != nil {
		return fmt.Errorf("sending %v to the receiver: %w", command["type"], err)
	}
	return ok(resp, fmt.Sprint(command["type"]))
}

func (v *videoV2) answered(id uint64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if ch, ok := v.waiting[id]; ok {
		close(ch)
		delete(v.waiting, id)
	}
}

// infoWait bounds how long a position waits for the receiver's fresh report before the last one stands.
const infoWait = time.Second

func (v *videoV2) position(ctx context.Context) (device.Playhead, error) {
	v.mu.Lock()
	v.asked++
	id := v.asked
	reply := make(chan struct{})
	v.waiting[id] = reply
	v.mu.Unlock()
	if err := v.command(ctx, map[string]any{"type": "playbackInfo", "kind": "request", "messageID": id}); err == nil {
		select {
		case <-reply:
		case <-time.After(infoWait):
		case <-ctx.Done():
		}
	}
	v.mu.Lock()
	delete(v.waiting, id)
	v.mu.Unlock()
	return v.head.position()
}

func (v *videoV2) setRate(ctx context.Context, rate float64) error {
	return v.command(ctx, map[string]any{"type": "setRate", "rate": rate})
}

func (v *videoV2) ending() *ending { return v.head.end }

// close leaves the receiver's player, which a receiver takes from the connection closing.
func (v *videoV2) close() {
	v.s.close()
	_ = v.s.l.Close()
}
