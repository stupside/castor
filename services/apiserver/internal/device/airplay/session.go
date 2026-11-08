package airplay

import (
	"context"
	"errors"
	"fmt"
	"sync"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

// player is one item handed to the receiver, on its own connection.
type player interface {
	position(context.Context) (device.Playhead, error)
	setRate(context.Context, float64) error
	ending() *ending
	close()
}

// receiver is what castor read of a receiver before casting to it.
type receiver struct {
	address  string
	name     string
	deviceID string
	features features
	status   uint64
	model    string
}

// session casts to one receiver, each Play on a connection of its own: a receiver queues a second item rather than playing it.
type session struct {
	r     receiver
	creds *Credentials
	// transient is whether castor pairs for each session without a PIN, where it holds no credentials.
	transient bool

	mu sync.Mutex
	// spare is the link Connect proved, which the first Play takes, with the secret its session keys come from.
	spare       *link
	spareSecret []byte
	current     player
	closed      bool
}

var _ device.Device = (*session)(nil)

// prove opens what castor's proof of pairing gives on l: the secret its session keys come from, nil where the receiver asks none.
func (s *session) prove(ctx context.Context, l *link) ([]byte, error) {
	switch {
	case s.creds != nil:
		return verifyPairing(ctx, l, *s.creds)
	case s.transient:
		return pairTransient(ctx, l)
	}
	return nil, nil
}

// open proves castor's pairing on l and seals l where the receiver plays within an AirPlay 2 session.
// An AirPlay 1 receiver checks the pairing and then speaks in the clear, as pyatv drives it, so its secret is nil.
func (s *session) open(ctx context.Context, l *link) ([]byte, error) {
	secret, err := s.prove(ctx, l)
	if err != nil {
		return nil, fmt.Errorf("verifying castor's pairing with %s: %w", s.r.name, err)
	}
	v2 := s.r.features.videoMethod() == methodVideoV2
	if secret != nil && (v2 || s.r.features.airPlay2()) {
		l.encrypt(controlKeys(secret))
	} else {
		secret = nil
	}
	if v2 && secret == nil {
		return nil, fmt.Errorf("%s plays video only within a paired session: %w", s.r.name, device.ErrPairingRequired)
	}
	return secret, nil
}

// connect opens a proven link to the receiver for one item.
func (s *session) connect(ctx context.Context) (*link, []byte, error) {
	l, err := dial(ctx, s.r.address)
	if err != nil {
		return nil, nil, &device.Gone{Device: s.r.name, Observed: "the AirPlay receiver did not accept a connection", Err: err}
	}
	secret, err := s.open(ctx, l)
	if err != nil {
		_ = l.Close()
		return nil, nil, err
	}
	return l, secret, nil
}

func (s *session) Play(ctx context.Context, playback device.Playback) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("the AirPlay session is closed")
	}
	previous, spare, spareSecret := s.current, s.spare, s.spareSecret
	s.current, s.spare, s.spareSecret = nil, nil, nil
	s.mu.Unlock()
	// Leaving the previous item first is what makes the receiver take the next one at once.
	if previous != nil {
		previous.close()
	}
	p, err := s.start(ctx, spare, spareSecret, playback)
	if err != nil && spare != nil && ctx.Err() == nil && !errors.Is(err, errRefused) {
		// The link Connect proved may have idled out while the stream was prepared: a fresh one decides.
		p, err = s.start(ctx, nil, nil, playback)
	}
	if err != nil {
		return fmt.Errorf("starting AirPlay playback: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		p.close()
		return errors.New("the AirPlay session is closed")
	}
	s.current = p
	return nil
}

// start hands playback to the receiver on l, or on a new link where l is nil.
func (s *session) start(ctx context.Context, l *link, secret []byte, playback device.Playback) (player, error) {
	if l == nil {
		var err error
		if l, secret, err = s.connect(ctx); err != nil {
			return nil, err
		}
	}
	var p player
	var err error
	switch s.r.features.videoMethod() {
	case methodVideoV2:
		p, err = playV2(ctx, l, secret, s.r.deviceID, playback)
	case methodVideoV1:
		p, err = playV1(ctx, l, secret, playback)
	default:
		err = errNoVideo
	}
	if err != nil {
		_ = l.Close()
		return nil, err
	}
	return p, nil
}

func (s *session) playing() (player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil, errors.New("the receiver plays nothing castor handed it")
	}
	return s.current, nil
}

func (s *session) AwaitEnd(ctx context.Context) error {
	p, err := s.playing()
	if err != nil {
		return err
	}
	select {
	case <-p.ending().done:
		return p.ending().err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *session) Pause(ctx context.Context) error  { return s.rate(ctx, 0, "pausing") }
func (s *session) Resume(ctx context.Context) error { return s.rate(ctx, 1, "resuming") }

func (s *session) rate(ctx context.Context, rate float64, doing string) error {
	p, err := s.playing()
	if err != nil {
		return err
	}
	if err := p.setRate(ctx, rate); err != nil {
		return fmt.Errorf("%s the AirPlay receiver: %w", doing, err)
	}
	return nil
}

// Stop leaves the receiver's player, which it takes from the session closing.
func (s *session) Stop(context.Context) error {
	s.mu.Lock()
	p := s.current
	s.mu.Unlock()
	if p != nil {
		p.ending().finish(nil)
		p.close()
	}
	return nil
}

func (s *session) Position(ctx context.Context) (device.Playhead, error) {
	p, err := s.playing()
	if err != nil {
		return device.Playhead{}, device.ErrNoPosition
	}
	return p.position(ctx)
}

func (s *session) Capabilities() *mediav1.Capabilities { return capabilities() }

func (s *session) Close() error {
	s.mu.Lock()
	s.closed = true
	p, spare := s.current, s.spare
	s.current, s.spare = nil, nil
	s.mu.Unlock()
	if p != nil {
		p.ending().finish(nil)
		p.close()
	}
	if spare != nil {
		_ = spare.Close()
	}
	return nil
}
