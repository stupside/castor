// Package cast casts a source through castor's public API and follows the cast to its end, or prints what it would play.
package cast

import (
	"context"
	"errors"
	"log/slog"
	"time"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/gen/castor/v1/castorv1connect"
)

// stopGrace is how long a stopped cast is given to release its device before the command returns.
const stopGrace = 10 * time.Second

var errStopped = errors.New("cast stopped")

// Run casts source to to and follows the cast to its end, showing the lines lines asks for; cancelling ctx stops it.
func Run(ctx context.Context, casts castorv1connect.CastServiceClient, to *castorv1.Target, source *castorv1.Source, lines Lines) error {
	started, err := casts.Cast(ctx, &castorv1.CastRequest{Target: to, Source: source})
	if err != nil {
		return err
	}
	id := started.GetCastId()
	server := FromServer(slog.Default().Handler())
	ended, err := follow(ctx, casts, id, lines.level(), server)
	if ctx.Err() == nil {
		return outcome(ended, err)
	}
	// Ctrl+C stops the cast, and waits for it to let go of the device.
	stopping, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopGrace)
	defer cancel()
	if _, err := casts.Stop(stopping, &castorv1.StopRequest{CastId: id}); err != nil {
		slog.DebugContext(ctx, "stopping cast", "error", err)
	}
	_, _ = follow(stopping, casts, id, nil, server)
	return context.Cause(ctx)
}

// follow watches cast id to its end, logging what changes and the lines it asked for.
func follow(ctx context.Context, casts castorv1connect.CastServiceClient, id string, logs *castorv1.LogLevel, server slog.Handler) (*castorv1.Ended, error) {
	stream, err := casts.Watch(ctx, &castorv1.WatchRequest{CastId: id, Logs: logs})
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	var last *castorv1.CastStatus
	for stream.Receive() {
		switch u := stream.Msg().GetUpdate().(type) {
		case *castorv1.WatchResponse_Status:
			changed(ctx, last, u.Status)
			last = u.Status
		case *castorv1.WatchResponse_Line:
			_ = server.Handle(ctx, record(u.Line))
		case *castorv1.WatchResponse_Ended:
			return u.Ended, nil
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("the watch ended without saying how the cast did")
}

// outcome is a cast's end as the command returns it.
func outcome(ended *castorv1.Ended, err error) error {
	if err != nil {
		return err
	}
	switch result := ended.GetResult().(type) {
	case *castorv1.Ended_Completed:
		return nil
	case *castorv1.Ended_Stopped:
		return errStopped
	case *castorv1.Ended_Failed:
		return errors.New(result.Failed.GetMessage())
	default:
		return errors.New("the cast ended without saying how it did")
	}
}
