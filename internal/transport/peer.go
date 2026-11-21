package transport

import (
	"context"
	"errors"
	"fmt"
	"io"

	"connectrpc.com/connect"
)

// Peer is a server this one calls, named in the errors its answers are reported with.
type Peer struct {
	Name     string
	TokenKey string
}

// Reported is err from the peer as this server reports it: a peer refusing this server is this server's fault, not the caller's.
func (p Peer) Reported(err error) error {
	switch connect.CodeOf(err) {
	case connect.CodeUnauthenticated, connect.CodePermissionDenied:
		return connect.NewError(connect.CodeInternal, fmt.Errorf("%s refused this server (check %s): %w", p.Name, p.TokenKey, err))
	case connect.CodeInvalidArgument:
		switch {
		case connect.IsWireError(err):
			return connect.NewError(connect.CodeInternal, fmt.Errorf("%s rejected this server's request: %w", p.Name, err))
		// The client reports a stream cut short as its own protocol error, which no server sent.
		case errors.Is(err, io.ErrUnexpectedEOF):
			return connect.NewError(connect.CodeUnavailable, fmt.Errorf("%s went away: %w", p.Name, err))
		}
	}
	return err
}

// Peering reports every error a client of peer meets through Reported, so no call has to.
func Peering(peer Peer) connect.Option { return connect.WithInterceptors(peerErrors{peer}) }

type peerErrors struct{ peer Peer }

func (i peerErrors) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		res, err := next(ctx, req)
		return res, i.peer.Reported(err)
	}
}

func (i peerErrors) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		return reportedConn{StreamingClientConn: next(ctx, spec), peer: i.peer}
	}
}

func (peerErrors) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// reportedConn reports what the peer says, which is where a cut stream is found.
type reportedConn struct {
	connect.StreamingClientConn
	peer Peer
}

func (c reportedConn) Receive(message any) error {
	return c.peer.Reported(c.StreamingClientConn.Receive(message))
}
