package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// shutdownGrace is how long requests and casts get to finish once a server is told to stop.
const shutdownGrace = 10 * time.Second

// Serve answers h on l until ctx ends, then closes l and runs drain beside the requests still finishing, within shutdownGrace.
func Serve(ctx context.Context, l net.Listener, h http.Handler, drain func(context.Context)) error {
	// Cleartext HTTP/2 beside HTTP/1.1, so gRPC tools reach reflection and health on the same port.
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, Protocols: &protocols}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(l) }()
	var serveErr error
	select {
	case err := <-served:
		serveErr = fmt.Errorf("serving %s: %w", l.Addr(), err)
	case <-ctx.Done():
	}
	grace, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	var draining sync.WaitGroup
	draining.Go(func() { drain(grace) })
	err := srv.Shutdown(grace)
	draining.Wait()
	if err != nil {
		// The grace ran out: what still runs is cut.
		return errors.Join(serveErr, srv.Close())
	}
	return serveErr
}

// Background serves h on l until stop, outliving ctx's cancellation so what it runs can still wind down.
func Background(ctx context.Context, l net.Listener, h http.Handler, drain func(context.Context)) (stop func()) {
	served, cancel := context.WithCancel(context.WithoutCancel(ctx))
	var serving sync.WaitGroup
	serving.Go(func() {
		if err := Serve(served, l, h, drain); err != nil {
			slog.ErrorContext(served, "background server stopped", "address", l.Addr().String(), "error", err)
		}
	})
	return func() {
		cancel()
		serving.Wait()
	}
}

// Listen listens on addr, warning when it answers beyond this machine without a token; key names the token's setting.
func Listen(ctx context.Context, addr, token, key string) (net.Listener, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", addr, err)
	}
	if tcp, ok := l.Addr().(*net.TCPAddr); token == "" && (!ok || !tcp.IP.IsLoopback()) {
		slog.WarnContext(ctx, "listening beyond this machine without a token: anyone on the network may use it", "address", l.Addr().String(), "set", key)
	}
	return l, nil
}

// Loopback serves h behind token on this machine only until stop, and is where it answers.
func Loopback(ctx context.Context, h http.Handler, token string, drain func(context.Context)) (Endpoint, func(), error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Endpoint{}, nil, fmt.Errorf("listening on loopback: %w", err)
	}
	stop := Background(ctx, l, Authorized(h, token), drain)
	return Endpoint{URL: "http://" + l.Addr().String(), Token: token}, stop, nil
}
