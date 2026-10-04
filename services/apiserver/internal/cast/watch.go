package cast

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/apiserver/internal/mediaclient"
)

// Watch sends the cast's status at once and on every change, its lines if asked, and how it ended last.
func (s *Service) Watch(ctx context.Context, req *castorv1.WatchRequest, out *connect.ServerStream[castorv1.WatchResponse]) error {
	c, err := s.find(req.GetCastId())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var lines chan *castorv1.LogLine
	if req.Logs != nil {
		lines = make(chan *castorv1.LogLine, 64)
		go c.forward(ctx, s.media, req.Logs, lines)
	}
	sendLine := func(line *castorv1.LogLine) error {
		return out.Send(&castorv1.WatchResponse{Update: &castorv1.WatchResponse_Line{Line: line}})
	}
	var sent *castorv1.CastStatus
	for {
		now, changed := c.now.Load()
		if !proto.Equal(now.status, sent) {
			if err := out.Send(&castorv1.WatchResponse{Update: &castorv1.WatchResponse_Status{Status: now.status}}); err != nil {
				return err
			}
			sent = now.status
		}
		if now.ended != nil {
			// Lines logged before the end go out before it does, for as long as the media server takes to send them.
			drained := time.After(stopGrace)
			for lines != nil {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case line, open := <-lines:
					if !open {
						lines = nil
					} else if err := sendLine(line); err != nil {
						return err
					}
				case <-drained:
					lines = nil
				}
			}
			return out.Send(&castorv1.WatchResponse{Update: &castorv1.WatchResponse_Ended{Ended: now.ended}})
		}
		select {
		case <-changed:
		case line, open := <-lines:
			if !open {
				lines = nil
			} else if err := sendLine(line); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// forward hands lines the media server's lines for c from logs on, once its cast there starts, until it ends.
func (c *cast) forward(ctx context.Context, media *mediaclient.Client, logs *castorv1.LogLevel, lines chan<- *castorv1.LogLine) {
	defer close(lines)
	now, changed := c.now.Load()
	for now.media == "" && now.ended == nil {
		select {
		case <-changed:
			now, changed = c.now.Load()
		case <-ctx.Done():
			return
		}
	}
	if now.media == "" {
		return
	}
	_, _ = media.Watch(ctx, now.media, logs, nil, func(line *castorv1.LogLine) {
		select {
		case lines <- line:
		case <-ctx.Done():
		}
	})
}
