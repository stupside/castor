package output

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Publication is a delivery served at a device-reachable URL until it is closed.
// Close unregisters the handler, cancels active requests and waits for them to
// return. Its owner can then release the handler's files and other resources.
// Repeated calls to Close are safe.
type Publication interface {
	URL(path string) *url.URL
	Close() error
}

// Publisher makes a delivery's HTTP handler reachable by a device.
// The caller owns the publication and must close it, including on cancellation.
// The publish context supplies values; Close controls the serving lifetime.
// Handlers must honor request cancellation when waiting for application data.
type Publisher interface {
	Publish(context.Context, http.Handler) (Publication, error)
}

// Static publishes one small document, such as a caption file, until the returned func closes it.
func Static(ctx context.Context, publisher Publisher, name, contentType, body string) (*url.URL, func() error, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /"+name, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		http.ServeContent(w, r, name, time.Time{}, strings.NewReader(body))
	})
	pub, err := publisher.Publish(ctx, mux)
	if err != nil {
		return nil, nil, err
	}
	return pub.URL(name), pub.Close, nil
}
