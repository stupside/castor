// Package apiserver is castor's API server, run as `castor api-server` or inside castor itself.
package apiserver

import (
	"context"
	"fmt"
	"net/http"

	"buf.build/go/protovalidate"

	"github.com/stupside/castor/gen/castor/v1/castorv1connect"
	"github.com/stupside/castor/internal/transport"
	"github.com/stupside/castor/services/apiserver/internal/cast"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/mediaclient"
)

// Backend is what the API server casts with, bound at the composition root.
type Backend struct {
	Devices  device.Registry
	Media    *mediaclient.Client
	Scraping cast.Resolver
}

// Server is the API server: the public API, for UIs and integrations.
type Server struct {
	http.Handler
	casts *cast.Service
}

// New serves the public contract with b, every cast asking what cfg says unless its request says otherwise.
func New(b Backend, cfg CastConfig) (*Server, error) {
	defaults := cfg.settings()
	// The contract alone states what a cast may ask, so the defaults are held to it before the first cast.
	if err := protovalidate.Validate(defaults); err != nil {
		return nil, fmt.Errorf("cast: %w", err)
	}
	devices := device.NewDirectory(b.Devices)
	casts := cast.New(defaults, devices, b.Media, b.Scraping)

	valid := transport.Checked()
	mux := http.NewServeMux()
	mux.Handle(castorv1connect.NewDeviceServiceHandler(devices, valid))
	mux.Handle(castorv1connect.NewCastServiceHandler(casts, valid))
	transport.Introspect(mux, castorv1connect.DeviceServiceName, castorv1connect.CastServiceName)
	return &Server{Handler: mux, casts: casts}, nil
}

// Shutdown takes no new cast, ends those running, failed as the server shuts down, and waits until they let go of their devices, or ctx ends.
func (s *Server) Shutdown(ctx context.Context) { s.casts.Drain(ctx) }
