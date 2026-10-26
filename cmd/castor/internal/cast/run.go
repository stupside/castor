// Package cast casts a source through castor's public API and follows the cast to its end, or prints what it would play.
package cast

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/gen/castor/v1/castorv1connect"
	"github.com/stupside/castor/internal/castid"
)

// stopGrace is how long a stopped cast is given to release its device before the command returns.
const stopGrace = 10 * time.Second

var errStopped = errors.New("cast stopped")

// Run casts source to to and follows the cast to its end, cancelling ctx stops it.
func Run(ctx context.Context, casts castorv1connect.CastServiceClient, to *castorv1.Target, source *castorv1.Source, info *castorv1.MediaInfo, start time.Duration) error {
	req := &castorv1.CastRequest{Target: to, Source: source, MediaInfo: info}
	if start > 0 {
		req.Start = durationpb.New(start)
	}
	started, err := casts.Cast(ctx, req)
	if err != nil {
		return err
	}
	id := started.GetCastId()
	ctx = castid.With(ctx, id)
	ended, err := follow(ctx, casts, id)
	if ctx.Err() == nil && err == nil {
		return outcome(ended)
	}
	// An interrupted command or broken watch stops the cast and waits for the device to be released.
	stopping, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopGrace)
	defer cancel()
	if _, err := casts.Stop(stopping, &castorv1.StopRequest{CastId: id}); err != nil {
		slog.WarnContext(ctx, "stopping the cast", "error", err)
	}
	_, _ = follow(stopping, casts, id)
	if ctx.Err() == nil {
		return err
	}
	return context.Cause(ctx)
}

// follow watches cast id to its end, logging what changes.
func follow(ctx context.Context, casts castorv1connect.CastServiceClient, id string) (*castorv1.Ended, error) {
	stream, err := casts.Watch(ctx, &castorv1.WatchRequest{CastId: id})
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
func outcome(ended *castorv1.Ended) error {
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
