// Package dlna is a UPnP MediaRenderer on one HTTP server: description, ConnectionManager and AVTransport.
package dlna

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/receiver"
	"github.com/stupside/castor/e2e/strategy"
)

const (
	avTransport       = "urn:schemas-upnp-org:service:AVTransport:1"
	connectionManager = "urn:schemas-upnp-org:service:ConnectionManager:1"
)

// Pin is how castor finds the device without multicast discovery.
type Pin string

const (
	// Description pins device.host to the description URL itself.
	Description Pin = "description"
	// SSDP pins device.host to a host:port castor sends one unicast M-SEARCH to.
	SSDP Pin = "ssdp"
)

// Settings are a DLNA device's own: what it advertises, what that means it decodes, and how castor pins it.
type Settings struct {
	Sink    string         `yaml:"sink"`
	Decodes receiver.Plays `yaml:"decodes"`
	Pin     Pin            `yaml:"pin"`
	// LockedFor answers the first n SetAVTransportURI with UPnP fault 705, as a transport still releasing its last media.
	LockedFor int     `yaml:"locked_for"`
	Fetches   Fetches `yaml:"fetches"`
}

// pinners serve each pin and return the device.host castor is given.
var pinners = map[Pin]func(t *testing.T, description string) (string, error){
	Description: func(_ *testing.T, description string) (string, error) { return description, nil },
	SSDP:        answerSearches,
}

type Family struct{}

func (Family) Name() string { return "dlna" }

func (Family) Build(raw yaml.Node) (receiver.Device, error) {
	settings := Settings{Pin: Description, Fetches: Once}
	if err := strategy.Decode(raw, &settings); err != nil {
		return nil, fmt.Errorf("dlna: %w", err)
	}
	if settings.Sink == "" || len(settings.Decodes.Video) == 0 {
		return nil, errors.New("dlna: a device needs a sink and what it decodes")
	}
	if _, ok := pinners[settings.Pin]; !ok {
		return nil, fmt.Errorf("dlna: pin %q: want %s or %s", settings.Pin, Description, SSDP)
	}
	if _, ok := fetchers[settings.Fetches]; !ok {
		return nil, fmt.Errorf("dlna: fetches %q: want %s or %s", settings.Fetches, Once, HeadProbeReplay)
	}
	return device{settings}, nil
}

type device struct{ Settings }

func (device) Type() string { return "dlna" }

func (d device) Start(t *testing.T, s *receiver.Session) (receiver.Endpoint, error) {
	// A renderer's end is polled from its transport state, so it plays for as long as its media lasts, as a real one does.
	s.PlayInRealtime()
	u := &upnp{session: s, sink: d.Sink, locked: d.LockedFor, fetch: fetchers[d.Fetches]}
	s.Check(func(declared string, h http.Header) []string {
		u.mu.Lock()
		meta := u.meta
		u.mu.Unlock()
		return streamHeaders(declared, didlFeatures(meta), h)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /description.xml", u.description)
	mux.HandleFunc("POST /control/{service}", u.control)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	host, err := pinners[d.Pin](t, server.URL+"/description.xml")
	if err != nil {
		return receiver.Endpoint{}, err
	}
	return receiver.Endpoint{Host: host, Plays: d.Decodes, EndsWithin: receiver.Reported}, nil
}
