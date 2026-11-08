package device

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

var (
	// ErrPairingRequired is a device that plays only for a client it paired with.
	ErrPairingRequired = errors.New("the device plays only for a client it paired with")

	// ErrWrongPIN is a PIN the device does not show.
	ErrWrongPIN = errors.New("the PIN does not match the one the device shows")
)

// Pairer is a family whose devices pair with castor before they play for it.
type Pairer interface {
	// BeginPairing asks the device at info to show a PIN.
	BeginPairing(ctx context.Context, info Info) (Pairing, error)
}

// Pairing is one pairing in progress, which the device shows a PIN for until Finish or Close.
type Pairing interface {
	// Finish proves pin and keeps what the device paired with; the pairing is over either way.
	Finish(ctx context.Context, pin string) error
	Close() error
}

// pairingLifetime bounds how long a device shows a PIN no client enters.
const pairingLifetime = 3 * time.Minute

type pendingPairing struct {
	Pairing
	expiry *time.Timer
}

type pairings struct {
	mu      sync.Mutex
	pending map[string]*pendingPairing
}

func (p *pairings) add(pairing Pairing) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[string]*pendingPairing{}
	}
	p.pending[id] = &pendingPairing{Pairing: pairing, expiry: time.AfterFunc(pairingLifetime, func() {
		if taken, ok := p.take(id); ok {
			_ = taken.Close()
		}
	})}
	return id
}

func (p *pairings) take(id string) (Pairing, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pending, ok := p.pending[id]
	if !ok {
		return nil, false
	}
	delete(p.pending, id)
	pending.expiry.Stop()
	return pending.Pairing, true
}

// StartPairing asks the target device to show a PIN.
func (d *Directory) StartPairing(ctx context.Context, req *castorv1.StartPairingRequest) (*castorv1.StartPairingResponse, error) {
	info, err := d.Target(ctx, req.GetTarget())
	if err != nil {
		return nil, err
	}
	f, err := d.reach.family(info.Type)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	pairer, ok := f.(Pairer)
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("%s devices do not pair", info.Type))
	}
	ctx, cancel := context.WithTimeout(ctx, d.reach.Timeout)
	defer cancel()
	located, err := locate(ctx, f, info)
	if err != nil {
		return nil, err
	}
	pairing, err := pairer.BeginPairing(ctx, located)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("asking the device for a PIN: %w", err))
	}
	return &castorv1.StartPairingResponse{PairingId: d.pairings.add(pairing)}, nil
}

// FinishPairing proves the PIN; a wrong one ends the pairing, and StartPairing shows a new PIN.
func (d *Directory) FinishPairing(ctx context.Context, req *castorv1.FinishPairingRequest) (*castorv1.FinishPairingResponse, error) {
	pairing, ok := d.pairings.take(req.GetPairingId())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no pairing in progress under this id: it finished or expired"))
	}
	if err := pairing.Finish(ctx, req.GetPin()); err != nil {
		if errors.Is(err, ErrWrongPIN) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("pairing with the device: %w", err))
	}
	return &castorv1.FinishPairingResponse{}, nil
}

func (d *Directory) CancelPairing(_ context.Context, req *castorv1.CancelPairingRequest) (*castorv1.CancelPairingResponse, error) {
	if pairing, ok := d.pairings.take(req.GetPairingId()); ok {
		_ = pairing.Close()
	}
	return &castorv1.CancelPairingResponse{}, nil
}
