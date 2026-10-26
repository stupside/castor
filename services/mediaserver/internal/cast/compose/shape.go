package compose

import (
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Shape is the device, the source, the ceiling and the operator's preference: pure data, no I/O.
type Shape struct {
	// Device is what the lent device decodes, and whether it fetches for itself.
	Device media.Capabilities

	// Program is the normalized set of inputs and selected tracks this cast reads.
	Program media.Program

	// Height is the picture the device would fetch, declared or probed; 0 is unmeasured.
	Height int

	// MaxHeight binds the composition, not just the encode.
	MaxHeight media.HeightCap

	// A hand-off cannot be lightened, so MaxBitrate binds it against Rate, the heaviest declared rung.
	MaxBitrate media.Bitrate
	Rate       media.Bitrate

	Delivery DeliveryPreference
}

// DeliveryPreference is whether a self-fetching device may be handed the source URL.
type DeliveryPreference string

const (
	// DeliveryAuto leaves the decision to the evidence (also the zero value).
	DeliveryAuto DeliveryPreference = "auto"
	// DeliveryServe refuses the handoff: castor reads the source and serves the device locally.
	DeliveryServe DeliveryPreference = "serve"
)

// handoff is a device that fetches for itself and plays the source as it is, within the ceiling.
func (s Shape) handoff() bool {
	primary, ok := s.Program.PrimaryInput()
	if !ok || s.Delivery == DeliveryServe {
		return false
	}
	probe, measured := s.Program.Measurement()
	return s.Device.SelfFetch && s.Program.SelfFetchable() && measured &&
		s.Device.CanPlaySourceIn(primary.ContentType, probe) &&
		(s.MaxHeight == 0 || !probe.HasVideo() || s.Height > 0 && s.MaxHeight.Admits(s.Height)) &&
		s.MaxBitrate.Admits(s.Rate)
}
