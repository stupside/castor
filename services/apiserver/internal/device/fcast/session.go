package fcast

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

// protocolVersion is the FCast version castor speaks; receivers that know v4 downgrade to it.
const protocolVersion = 3

// session is one FCast connection to a receiver, following the Play it was last handed.
type session struct {
	ch   *channel
	name string
	// announced carries the version the receiver states on connecting.
	announced chan uint64

	mu      sync.Mutex
	current *playback
}

var _ device.Device = (*session)(nil)

type versionMessage struct {
	Version uint64 `json:"version"`
}

type initialMessage struct {
	AppName string `json:"appName"`
}

type subscribeMessage struct {
	Event struct {
		Type int `json:"type"`
	} `json:"event"`
}

// playMessage leaves out volume, speed and time: a receiver applies whatever it is sent, zero too.
type playMessage struct {
	Container string    `json:"container"`
	URL       string    `json:"url"`
	Metadata  *metadata `json:"metadata,omitempty"`
}

// metadata is the protocol's generic kind; receivers reject one without its type, so the zero is sent.
type metadata struct {
	Type         int    `json:"type"`
	Title        string `json:"title,omitempty"`
	ThumbnailURL string `json:"thumbnailUrl,omitempty"`
}

// open takes conn through the v3 handshake.
func open(ctx context.Context, conn net.Conn, name string) (*session, error) {
	s := &session{name: name, announced: make(chan uint64, 1)}
	s.ch = newChannel(conn, s.observe)
	ctx, cancel := context.WithTimeoutCause(ctx, answerWithin, fmt.Errorf("the fcast receiver did not finish the handshake within %s: %w", answerWithin, context.DeadlineExceeded))
	defer cancel()
	if err := s.handshake(ctx); err != nil {
		_ = s.ch.Close()
		return nil, fmt.Errorf("connecting to fcast receiver: %w", err)
	}
	return s, nil
}

func (s *session) handshake(ctx context.Context) error {
	// Version goes first: a receiver takes any other opening packet for a v1 sender.
	if err := s.ch.sendJSON(ctx, opVersion, versionMessage{Version: protocolVersion}); err != nil {
		return err
	}
	var theirs uint64
	select {
	case theirs = <-s.announced:
	case <-s.ch.gone:
		return errors.New("the receiver closed the connection before announcing its version")
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	if theirs < protocolVersion {
		return fmt.Errorf("the receiver speaks FCast v%d; castor needs v%d", theirs, protocolVersion)
	}
	if err := s.ch.sendJSON(ctx, opInitial, initialMessage{AppName: "Castor"}); err != nil {
		return err
	}
	// Receivers report a finished item as paused; only this event tells its end.
	var subscribe subscribeMessage
	subscribe.Event.Type = eventMediaItemEnd
	return s.ch.sendJSON(ctx, opSubscribeEvent, subscribe)
}

// observe routes what the receiver says, from the reader's goroutine.
func (s *session) observe(op opcode, body []byte) {
	if op == opVersion {
		var v versionMessage
		if json.Unmarshal(body, &v) == nil {
			select {
			case s.announced <- v.Version:
			default:
			}
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.current; p != nil && p.observe(op, body) {
		time.AfterFunc(idleSettle, func() { s.settle(p) })
	}
}

// settle ends p as stopped if it is still idle, no error having followed.
func (s *session) settle(p *playback) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.idle {
		p.ending.finish(nil)
	}
}

// Play returns once the receiver echoes the media back, so a refused one fails the hand-off; a dropped connection is gone.
func (s *session) Play(ctx context.Context, playback device.Playback) error {
	if err := s.play(ctx, playback); err != nil {
		select {
		case <-s.ch.gone:
			return &device.Gone{Device: s.name, Observed: "the FCast connection closed before the receiver took the media", Err: err}
		default:
			return err
		}
	}
	return nil
}

func (s *session) play(ctx context.Context, playback device.Playback) error {
	stream, info := playback.URL.String(), playback.Info
	container := device.MIME(playback.Container())
	if container == "" {
		return fmt.Errorf("fcast has no name for container %v", playback.Container())
	}
	message := playMessage{Container: container, URL: stream}
	if info.GetTitle() != "" || info.GetImageUrl() != "" {
		message.Metadata = &metadata{Title: info.GetTitle(), ThumbnailURL: info.GetImageUrl()}
	}
	mine := newPlayback(stream)
	s.mu.Lock()
	s.current = mine
	s.mu.Unlock()
	err := s.ch.sendJSON(ctx, opPlay, message)
	if err == nil {
		err = s.verdict(ctx, mine)
	}
	if err != nil {
		s.mu.Lock()
		// A Play begun since owns the watch now; this one's failure is not its to clear.
		if s.current == mine {
			s.current = nil
		}
		s.mu.Unlock()
		return fmt.Errorf("starting fcast playback: %w", err)
	}
	return nil
}

func (s *session) verdict(ctx context.Context, p *playback) error {
	ctx, cancel := context.WithTimeoutCause(ctx, answerWithin, fmt.Errorf("the fcast receiver did not take the media within %s: %w", answerWithin, context.DeadlineExceeded))
	defer cancel()
	select {
	case err := <-p.verdict:
		return err
	case <-s.ch.gone:
		return errors.New("the fcast receiver closed the connection before taking the media")
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *session) Pause(ctx context.Context) error { return s.command(ctx, opPause, "pausing") }

func (s *session) Resume(ctx context.Context) error { return s.command(ctx, opResume, "resuming") }

// Stop ends what the receiver plays, whoever handed it.
func (s *session) Stop(ctx context.Context) error { return s.command(ctx, opStop, "stopping") }

func (s *session) command(ctx context.Context, op opcode, doing string) error {
	if err := s.ch.send(ctx, op, nil); err != nil {
		return fmt.Errorf("%s the fcast receiver: %w", doing, err)
	}
	return nil
}

// Position is the last one the receiver reported for the media castor handed it, which it pushes every second or so.
func (s *session) Position(context.Context) (device.Playhead, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.current; p != nil && p.placed {
		return p.head, nil
	}
	return device.Playhead{}, device.ErrNoPosition
}

func (s *session) Close() error {
	return s.ch.Close()
}

// Capabilities is what every FCast receiver app is expected to play; v3 carries no subtitle tracks, so captions are burnt in.
func (s *session) Capabilities() *mediav1.Capabilities {
	caps := &mediav1.Capabilities{Provenance: "FCast v3 receiver family envelopes; receiver app and device limits unverified", SelfFetch: true}
	for _, audio := range []mediav1.AudioCodec{mediav1.AudioCodec_AUDIO_CODEC_AAC, mediav1.AudioCodec_AUDIO_CODEC_MP3} {
		tracks := &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: device.H264Support(), Audio: &mediav1.AudioSupport{Codec: audio, MaxChannels: 2}}}
		caps.Formats = append(caps.Formats,
			&mediav1.FormatSupport{Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{}}, Tracks: tracks},
			&mediav1.FormatSupport{Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MP4}}, Tracks: tracks},
		)
	}
	return caps
}
