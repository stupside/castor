// Package mediaserver is castor's media server, run as `castor media-server` or inside castor itself.
package mediaserver

import (
	"context"
	"net/http"
	"net/url"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/gen/castor/media/v1/mediav1connect"
	"github.com/stupside/castor/internal/transport"
	"github.com/stupside/castor/services/mediaserver/internal/cast"
	"github.com/stupside/castor/services/mediaserver/internal/castlog"
	"github.com/stupside/castor/services/mediaserver/internal/mediaroute"
)

// Backend is what the media server casts with, bound at the composition root.
type Backend struct {
	// Caster binds one cast to what it asked.
	Caster func(asked *mediav1.PlaybackSettings) cast.Caster
}

// Server is the media server: its API, for the API server alone, and its media route, for devices.
type Server struct {
	API   http.Handler
	Media http.Handler
	casts *cast.Service
}

// New serves the media contract with b, telling devices to reach its media route at reach.
func New(b Backend, reach *url.URL) *Server {
	casts := cast.New(b.Caster, reach)

	valid := transport.Checked()
	api := http.NewServeMux()
	api.Handle(mediav1connect.NewCastServiceHandler(casts, valid))
	api.Handle(mediav1connect.NewDeviceServiceHandler(casts, valid))
	api.Handle(mediav1connect.NewStreamServiceHandler(casts, valid))
	transport.Introspect(api, mediav1connect.CastServiceName, mediav1connect.DeviceServiceName, mediav1connect.StreamServiceName)
	media := http.NewServeMux()
	media.Handle(mediaroute.Pattern, mediaroute.Handler(casts.Serves))
	return &Server{API: castlog.Served(api), Media: castlog.Served(media), casts: casts}
}

// Shutdown takes no new cast, ends those running, failed as the server shuts down, and waits until they let go of what they hold, or ctx ends.
func (s *Server) Shutdown(ctx context.Context) { s.casts.Drain(ctx) }
