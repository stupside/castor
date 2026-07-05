package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/stupside/castor/cmd"
)

func main() {
	level := log.InfoLevel
	if slices.Contains(os.Args, "--debug") {
		level = log.DebugLevel
	}
	slog.SetDefault(slog.New(
		log.NewWithOptions(os.Stderr, log.Options{
			ReportTimestamp: true,
			TimeFormat:      "15:04:05.000",
			Level:           level,
		}),
	))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cmd.Root()

	if err := root.Run(ctx, os.Args); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			slog.InfoContext(ctx, "shutting down", "cause", cause)
			return
		}
		slog.ErrorContext(ctx, "application error", "error", err)
		os.Exit(1)
	}
}

