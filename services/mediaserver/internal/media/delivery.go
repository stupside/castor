package media

// Fidelity is how much of the source a device is handed unchanged, least first.
type Fidelity uint8

const (
	// FidelityConverted re-encodes or drops at least one track.
	FidelityConverted Fidelity = iota + 1
	// FidelityOriginalTracks copies every track into another container, which can drop signaling such as Dolby Vision.
	FidelityOriginalTracks
	// FidelityOriginalFile hands over the source file itself.
	FidelityOriginalFile
)

// Delivery is what one attempt handed its device.
type Delivery struct {
	Fidelity Fidelity
	// DolbyVision is a source declaring Dolby Vision, which only the original file is sure to keep signaled.
	DolbyVision bool
}
