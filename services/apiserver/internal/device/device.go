// Package device is the port every device family implements, the registry reaching them, and the directory the API lists and lends them from.
package device

import (
	"context"
	"net/url"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// Type names a device family in configuration and inside the registry.
type Type string

// Device is a connected device, ready to play; whoever connected it closes it.
type Device interface {
	// Play points the device at streamURL, packaged as container.
	Play(ctx context.Context, streamURL *url.URL, container mediav1.Container) error

	AwaitEnd(ctx context.Context) error

	Capabilities() *mediav1.Capabilities

	Close() error
}

// Info names one device, where it answers.
type Info struct {
	// ID is the device's own identity, the same on every discovery and unique within its family.
	ID      string
	Name    string
	Type    Type
	Address string
}

// Family is one device family's strategy: everything protocol-specific behind a single interface.
type Family interface {
	// Type is the name the contract selects this family by.
	Type() Type

	// Discover is every device answering now, each with its ID; one announced twice may repeat.
	Discover(ctx context.Context) []Info

	// Locate resolves a pinned address into the one Connect dials.
	Locate(ctx context.Context, address string) (string, error)

	// Connect opens a session to the device at info.
	Connect(ctx context.Context, info Info) (Device, error)
}

// id is how the contract names a discovered device: its family, then its own identity; a pinned one has none.
func (i Info) id() string {
	if i.ID == "" {
		return ""
	}
	return string(i.Type) + ":" + i.ID
}

// Public is the device as the contract shows it.
func (i Info) Public() *castorv1.Device {
	return &castorv1.Device{Id: i.id(), Name: i.Name, Type: i.Type.public(), Address: i.Address}
}

var publicTypes = map[Type]castorv1.DeviceType{
	"dlna":       castorv1.DeviceType_DEVICE_TYPE_DLNA,
	"chromecast": castorv1.DeviceType_DEVICE_TYPE_CHROMECAST,
	"roku":       castorv1.DeviceType_DEVICE_TYPE_ROKU,
}

func (t Type) public() castorv1.DeviceType { return publicTypes[t] }
