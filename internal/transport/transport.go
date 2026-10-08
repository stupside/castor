// Package transport is how castor's processes talk over HTTP: serving with a graceful end, a bearer token on both sides.
package transport

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"connectrpc.com/grpcreflect"
	"connectrpc.com/validate"
)

// Endpoint is where a server answers, and the token it asks for.
type Endpoint struct {
	URL   string
	Token string
}

// Client is a client of e, carrying its token.
func (e Endpoint) Client() *http.Client { return Bearer(e.Token) }

// Checked holds every message a handler or client takes and sends to the rules its contract states.
func Checked() connect.Option {
	return connect.WithInterceptors(validate.NewInterceptor(validate.WithValidateResponses()))
}

// Introspect serves health checks and reflection for services on mux.
func Introspect(mux *http.ServeMux, services ...string) {
	mux.Handle(grpchealth.NewHandler(grpchealth.NewStaticChecker(services...)))
	reflector := grpcreflect.NewStaticReflector(services...)
	mux.Handle(grpcreflect.NewHandlerV1(reflector))
	mux.Handle(grpcreflect.NewHandlerV1Alpha(reflector))
}
