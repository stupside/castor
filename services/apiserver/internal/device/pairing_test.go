package device

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// pairingFamily is a family whose devices show the PIN 0427.
type pairingFamily struct {
	stub
	mu     sync.Mutex
	shown  int
	closed int
	paired []string
}

func (*pairingFamily) Type() Type { return "airplay" }

func (f *pairingFamily) BeginPairing(_ context.Context, info Info) (Pairing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shown++
	return &shownPIN{f: f, address: info.Address}, nil
}

type shownPIN struct {
	f       *pairingFamily
	address string
}

func (p *shownPIN) Finish(_ context.Context, pin string) error {
	defer p.Close()
	if pin != "0427" {
		return ErrWrongPIN
	}
	p.f.mu.Lock()
	defer p.f.mu.Unlock()
	p.f.paired = append(p.f.paired, p.address)
	return nil
}

func (p *shownPIN) Close() error {
	p.f.mu.Lock()
	defer p.f.mu.Unlock()
	p.f.closed++
	return nil
}

func pinnedTV() *castorv1.Target {
	return &castorv1.Target{Target: &castorv1.Target_Pinned_{Pinned: &castorv1.Target_Pinned{Type: castorv1.DeviceType_DEVICE_TYPE_AIRPLAY, Address: "10.0.0.7"}}}
}

func TestAPairingIsFinishedWithThePINTheDeviceShows(t *testing.T) {
	f := &pairingFamily{}
	d := NewDirectory(Registry{Families: []Family{f}, Timeout: time.Second}, nil)

	started, err := d.StartPairing(t.Context(), &castorv1.StartPairingRequest{Target: pinnedTV()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.FinishPairing(t.Context(), &castorv1.FinishPairingRequest{PairingId: started.GetPairingId(), Pin: "0428"})
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !errors.Is(err, ErrWrongPIN) {
		t.Fatalf("a wrong PIN = %v, want it refused as invalid", err)
	}
	// A wrong PIN ends that pairing: the device shows a new one for the next.
	if _, err := d.FinishPairing(t.Context(), &castorv1.FinishPairingRequest{PairingId: started.GetPairingId(), Pin: "0427"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("a pairing finished twice = %v", err)
	}
	again, err := d.StartPairing(t.Context(), &castorv1.StartPairingRequest{Target: pinnedTV()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.FinishPairing(t.Context(), &castorv1.FinishPairingRequest{PairingId: again.GetPairingId(), Pin: "0427"}); err != nil {
		t.Fatal(err)
	}
	if len(f.paired) != 1 || f.paired[0] != "10.0.0.7" || f.shown != 2 {
		t.Errorf("paired %v after %d PINs", f.paired, f.shown)
	}
}

func TestACancelledPairingStopsShowingItsPIN(t *testing.T) {
	f := &pairingFamily{}
	d := NewDirectory(Registry{Families: []Family{f}, Timeout: time.Second}, nil)
	started, err := d.StartPairing(t.Context(), &castorv1.StartPairingRequest{Target: pinnedTV()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.CancelPairing(t.Context(), &castorv1.CancelPairingRequest{PairingId: started.GetPairingId()}); err != nil {
		t.Fatal(err)
	}
	if f.closed != 1 {
		t.Errorf("closed %d pairings, want the cancelled one", f.closed)
	}
	if _, err := d.FinishPairing(t.Context(), &castorv1.FinishPairingRequest{PairingId: started.GetPairingId(), Pin: "0427"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("a cancelled pairing finished: %v", err)
	}
}

func TestAFamilyThatDoesNotPairSaysSo(t *testing.T) {
	d := NewDirectory(Registry{Families: []Family{controlledFamily{connected: new(bool)}}, Timeout: time.Second}, nil)
	target := &castorv1.Target{Target: &castorv1.Target_Pinned_{Pinned: &castorv1.Target_Pinned{Type: castorv1.DeviceType_DEVICE_TYPE_DLNA, Address: "10.0.0.8"}}}
	if _, err := d.StartPairing(t.Context(), &castorv1.StartPairingRequest{Target: target}); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Errorf("StartPairing on DLNA = %v, want unimplemented", err)
	}
}
