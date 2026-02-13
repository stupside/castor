package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/app"
	"github.com/stupside/castor/internal/device"
)

// scanCommand returns the "scan" CLI subcommand.
func scanCommand(_ context.Context) *cli.Command {
	return &cli.Command{
		Name:  "scan",
		Usage: "List all devices on the local network",
		Flags: []cli.Flag{},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := app.ConfigFrom(cmd)
			if err != nil {
				return err
			}

			iface, err := net.InterfaceByName(cfg.Network.Interface)
			if err != nil {
				return fmt.Errorf("looking up interface %q: %w", cfg.Network.Interface, err)
			}

			devices, err := device.Discover(ctx, iface, cfg.Network.Timeout)
			if err != nil {
				return fmt.Errorf("scan failed: %w", err)
			}

			if len(devices) == 0 {
				slog.Info("no devices found")
				return nil
			}

			slog.Info("scan complete", "count", len(devices))
			for _, d := range devices {
				slog.Info("device found", "name", d.Name, "type", d.Type, "address", d.Address)
			}
			return nil
		},
	}
}
