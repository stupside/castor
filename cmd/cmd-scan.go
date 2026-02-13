package cmd

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/app"
	"github.com/stupside/castor/internal/device"
)

// scanCommand returns the "scan" CLI subcommand.
func scanCommand() *cli.Command {
	return &cli.Command{
		Name:  "scan",
		Usage: "List all devices on the local network",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := app.ConfigFrom(cmd)
			if err != nil {
				return err
			}

			devices, err := device.Discover(ctx, cfg.Network.Timeout)
			if err != nil {
				return fmt.Errorf("scan failed: %w", err)
			}

			if len(devices) == 0 {
				fmt.Println("no devices found")
				return nil
			}

			for _, d := range devices {
				fmt.Printf("%s\t%s\t%s\n", d.Name, d.Type, d.Address)
			}
			return nil
		},
	}
}
