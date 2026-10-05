package transport

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
)

type failedListener struct{ err error }

func (l failedListener) Accept() (net.Conn, error) { return nil, l.err }
func (failedListener) Close() error                { return nil }
func (failedListener) Addr() net.Addr              { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234} }

func TestListenerFailureStillDrainsServerWork(t *testing.T) {
	failure := errors.New("listener failed")
	drained := false
	err := Serve(t.Context(), failedListener{failure}, http.NotFoundHandler(), func(ctx context.Context) {
		if ctx.Err() != nil {
			t.Error("cleanup was not given a usable grace context")
		}
		drained = true
	})
	if !errors.Is(err, failure) {
		t.Fatalf("Serve = %v, want the listener failure", err)
	}
	if !drained {
		t.Fatal("the listener failed and abandoned running server work")
	}
}
