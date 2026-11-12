package cast

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// pairingDevices is a device service whose TV shows the PIN 0427.
type pairingDevices struct {
	listedDevices
	finished, cancelled []string
}

func (*pairingDevices) StartPairing(context.Context, *castorv1.StartPairingRequest) (*castorv1.StartPairingResponse, error) {
	return &castorv1.StartPairingResponse{PairingId: "p1"}, nil
}

func (d *pairingDevices) FinishPairing(_ context.Context, req *castorv1.FinishPairingRequest) (*castorv1.FinishPairingResponse, error) {
	if req.GetPin() != "0427" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("wrong PIN"))
	}
	d.finished = append(d.finished, req.GetPairingId())
	return &castorv1.FinishPairingResponse{}, nil
}

func (d *pairingDevices) CancelPairing(_ context.Context, req *castorv1.CancelPairingRequest) (*castorv1.CancelPairingResponse, error) {
	d.cancelled = append(d.cancelled, req.GetPairingId())
	return &castorv1.CancelPairingResponse{}, nil
}

func TestACastRefusedForPairingSaysSo(t *testing.T) {
	ended := &castorv1.Ended{Result: &castorv1.Ended_Failed{Failed: &castorv1.Failure{Code: castorv1.FailureCode_FAILURE_CODE_PAIRING_REQUIRED, Message: "the TV asks castor to pair with it first"}}}
	if err := outcome(ended); !errors.Is(err, ErrPairingRequired) {
		t.Errorf("outcome = %v, want pairing required", err)
	}
}

func TestPairProvesThePINTheUserTypes(t *testing.T) {
	devices := &pairingDevices{}
	if err := Pair(t.Context(), devices, nil, func(context.Context) (string, error) { return "0427", nil }); err != nil {
		t.Fatal(err)
	}
	if len(devices.finished) != 1 || devices.finished[0] != "p1" {
		t.Errorf("finished %v", devices.finished)
	}
}

func TestAPINNobodyTypesCancelsThePairing(t *testing.T) {
	devices := &pairingDevices{}
	gaveUp := errors.New("no PIN entered")
	if err := Pair(t.Context(), devices, nil, func(context.Context) (string, error) { return "", gaveUp }); !errors.Is(err, gaveUp) {
		t.Fatalf("Pair = %v", err)
	}
	if len(devices.cancelled) != 1 {
		t.Error("the TV kept showing its PIN after the user gave up")
	}
}
