// Package transport is how castor's processes talk over HTTP: serving with a graceful end, a bearer token on both sides.
package transport

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"connectrpc.com/grpcreflect"
	"connectrpc.com/validate"

	"github.com/stupside/castor/internal/castid"
	"github.com/stupside/castor/internal/telemetry"
)

// Endpoint is where a server answers, and the token it asks for; a service's own command reads only its token.
type Endpoint struct {
	URL   string `yaml:"url" validate:"omitempty,http_url"`
	Token string `yaml:"token"`
}

// Client is a client of e, carrying its token.
func (e Endpoint) Client() *http.Client { return Bearer(e.Token) }

// Options hold every message a handler or client takes and sends to the rules its contract states, and carry the id of the cast a call belongs to.
func Options() connect.Option {
	return connect.WithInterceptors(castid.Interceptor(), telemetry.RPC(), validate.NewInterceptor(validate.WithValidateResponses()))
}

// Introspect serves health checks and reflection for services on mux.
func Introspect(mux *http.ServeMux, services ...string) {
	mux.Handle(grpchealth.NewHandler(grpchealth.NewStaticChecker(services...)))
	reflector := grpcreflect.NewStaticReflector(services...)
	mux.Handle(grpcreflect.NewHandlerV1(reflector))
	mux.Handle(grpcreflect.NewHandlerV1Alpha(reflector))
}
