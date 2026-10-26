// Package device discovers devices, connects through their protocol families, and manages API-visible device ownership.
package device

import (
	"cmp"
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// Type names a device family in configuration and inside the registry.
type Type string

// Playback is the play a cast asked for, its URL as the device reaches it, and what the viewer is told of it.
type Playback struct {
	URL  *url.URL
	Play *mediav1.DeviceCommand_Play
	Info *castorv1.MediaInfo
}

// Container is how the stream is packaged, whichever way it reaches the device.
func (p Playback) Container() mediav1.Container {
	if p.Play.GetPlaylist() != nil {
		return mediav1.Container_CONTAINER_HLS
	}
	return cmp.Or(p.Play.GetSource().GetContainer(), p.Play.GetProgressive().GetContainer())
}

// Duration is the stream's runtime, zero where the source declared none.
func (p Playback) Duration() time.Duration {
	return cmp.Or(p.Play.GetSource().GetDuration(), p.Play.GetProgressive().GetRuntime().GetDuration(), p.Play.GetPlaylist().GetDuration()).AsDuration()
}

// Device is a connected device, ready to play; whoever connected it closes it.
type Device interface {
	// Play points the device at the requested media.
	Play(ctx context.Context, playback Playback) error

	AwaitEnd(ctx context.Context) error

	// Pause holds the device on its picture until Resume, for a source that fell behind playback.
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error

	// Stop ends what the device plays, so a cast that ends while the device still fetches from elsewhere does not leave it playing.
	Stop(ctx context.Context) error

	// Position is where the device says it has played to, ErrNoPosition where it cannot say.
	Position(ctx context.Context) (Playhead, error)

	Capabilities() *mediav1.Capabilities

	Close() error
}

var ErrNoPosition = errors.New("the device does not report its position")

type Playhead struct {
	At     time.Duration
	Paused bool
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

// public is the contract's name for t, which spells it as the enum's name without its prefix.
func (t Type) public() castorv1.DeviceType {
	return castorv1.DeviceType(castorv1.DeviceType_value[deviceTypePrefix+strings.ToUpper(string(t))])
}

const deviceTypePrefix = "DEVICE_TYPE_"

// WithPort completes a bare host, bracketed or not, with a family's default port.
func WithPort(address, port string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(strings.Trim(address, "[]"), port)
}
