package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/stupside/castor/cmd"
)

func main() {
	if slices.Contains(os.Args, "--debug") {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cmd.Root()

	if err := root.Run(ctx, os.Args); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			slog.InfoContext(ctx, "shutting down", "cause", cause)
		} else {
			slog.Error("application error", "error", err)
			os.Exit(1)
		}
	}
}
