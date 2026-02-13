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

	// After the first signal cancels the context, restore default signal
	// handling so a second signal terminates the process immediately.
	go func() {
		<-ctx.Done()
		stop()
	}()

	root := cmd.Root(ctx)

	if err := root.Run(ctx, os.Args); err != nil {
		slog.Error("application error", "error", err)
		os.Exit(1)
	}
}
