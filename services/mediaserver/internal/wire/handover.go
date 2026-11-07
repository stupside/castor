package wire

import (
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Handover is what an attempt handed its device, on the wire.
func Handover(d media.Delivery) *castorv1.Handover {
	return &castorv1.Handover{Fidelity: fidelities[d.Fidelity], DolbyVision: d.DolbyVision}
}

var fidelities = map[media.Fidelity]castorv1.Fidelity{
	media.FidelityConverted:      castorv1.Fidelity_FIDELITY_CONVERTED,
	media.FidelityOriginalTracks: castorv1.Fidelity_FIDELITY_ORIGINAL_TRACKS,
	media.FidelityOriginalFile:   castorv1.Fidelity_FIDELITY_ORIGINAL_FILE,
}
