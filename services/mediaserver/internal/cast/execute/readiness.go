package execute

import (
	"context"
	"fmt"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// presentationReadiness records the evidence preparation acquired before a
// device may receive a URL. The two paths have different evidence: a source
// handoff is eligible for self-fetch, while a served output was validated.
type presentationReadiness uint8

const (
	sourceEligible presentationReadiness = iota + 1
	outputValidated
)

// readyPresentation is the only presentation the coordinator may hand to a
// device. Preparation can retain a partial presentation for cleanup and
// failure evidence without making it ready for playback.
type readyPresentation struct{ presentation }

func certifyPresentation(p presentation) (readyPresentation, error) {
	if p.target == nil || p.contentType == "" {
		return readyPresentation{}, fmt.Errorf("presentation has no playable target")
	}
	switch p.readiness {
	case sourceEligible:
		if !p.source || p.delivery != nil {
			return readyPresentation{}, fmt.Errorf("source handoff has inconsistent delivery")
		}
	case outputValidated:
		if p.source || p.delivery == nil {
			return readyPresentation{}, fmt.Errorf("validated output has inconsistent delivery")
		}
	default:
		return readyPresentation{}, fmt.Errorf("presentation has no readiness evidence")
	}
	return readyPresentation{presentation: p}, nil
}

func (p readyPresentation) play(ctx context.Context, device Device) error {
	return hand(ctx, device, Playback{URL: p.target, Captions: p.captions, Stream: p.stream()})
}

// stream is how what the device is handed reaches it.
func (p presentation) stream() Stream {
	switch {
	case p.source:
		return Source{ContentType: p.contentType, Runtime: p.runtime}
	case p.contentType == media.HLS:
		return Playlist{Runtime: p.runtime}
	}
	served := Served{ContentType: p.contentType}
	if p.runtime > 0 {
		served.Runtime = &Runtime{Length: p.runtime, TimeSeek: p.delivery != nil && p.delivery.sought != nil}
	}
	return served
}
