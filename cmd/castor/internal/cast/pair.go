package cast

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/gen/castor/v1/castorv1connect"
)

// ErrPairingRequired is a cast whose device plays only for a client castor paired with it.
var ErrPairingRequired = errors.New("the device asks castor to pair with it first")

// PIN reads the PIN a device shows from whoever watches it.
type PIN func(context.Context) (string, error)

// Paired casts as Run does, pairing castor with the device first where it asks for it; pin nil only reports that.
func Paired(ctx context.Context, casts castorv1connect.CastServiceClient, devices castorv1connect.DeviceServiceClient, to *castorv1.Target, source *castorv1.Source, info *castorv1.MediaInfo, start time.Duration, pin PIN) error {
	err := Run(ctx, casts, to, source, info, start)
	if !errors.Is(err, ErrPairingRequired) || pin == nil {
		return err
	}
	if err := Pair(ctx, devices, to, pin); err != nil {
		return fmt.Errorf("pairing with the device: %w", err)
	}
	return Run(ctx, casts, to, source, info, start)
}

// Pair asks the device to show a PIN and proves the one pin reads.
func Pair(ctx context.Context, devices castorv1connect.DeviceServiceClient, to *castorv1.Target, pin PIN) error {
	started, err := devices.StartPairing(ctx, &castorv1.StartPairingRequest{Target: to})
	if err != nil {
		return err
	}
	id := started.GetPairingId()
	entered, err := pin(ctx)
	if err != nil {
		_, _ = devices.CancelPairing(context.WithoutCancel(ctx), &castorv1.CancelPairingRequest{PairingId: id})
		return err
	}
	_, err = devices.FinishPairing(ctx, &castorv1.FinishPairingRequest{PairingId: id, Pin: entered})
	return err
}

// TerminalPIN asks for the PIN on a terminal; nil where in is not one, as nobody could answer.
func TerminalPIN(in *os.File, out io.Writer) PIN {
	if stat, err := in.Stat(); err != nil || stat.Mode()&os.ModeCharDevice == 0 {
		return nil
	}
	return func(ctx context.Context) (string, error) {
		fmt.Fprint(out, "Enter the PIN the TV shows: ")
		line := make(chan string, 1)
		go func() {
			text, _ := bufio.NewReader(in).ReadString('\n')
			line <- strings.TrimSpace(text)
		}()
		select {
		case pin := <-line:
			if pin == "" {
				return "", errors.New("no PIN entered")
			}
			return pin, nil
		case <-ctx.Done():
			return "", context.Cause(ctx)
		}
	}
}
