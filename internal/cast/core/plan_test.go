package core

import (
	"fmt"
	"testing"

	"github.com/stupside/castor/internal/cast/subtitle"
	"github.com/stupside/castor/internal/media"
)

// mpegtsContentType is what a served cast that is not the self-fetch mp4 remux
// tells the device it is fetching.
const mpegtsContentType = media.MPEGTS

// TestNewPlan pins the pure plan decision that replaced the two per-device
// strategies: given the renderer's advertised capabilities (SelfFetch + accepted
// containers), the source container, and whether the transcriber is enabled, the
// three probe-free axes (Delivery, Subtitle, OutputContentType) must come out
// exactly as the old DLNA and Chromecast paths behaved. It is the plan-level
// heir to the deleted renderer strategy tests.
func TestNewPlan(t *testing.T) {
	// caps builds a renderer that self-fetches (or not) and accepts the given
	// containers. The audio/video support lists are irrelevant to the plan (they
	// feed the executor's copy-vs-encode, tested in resolve_test), so they stay
	// empty here. A served renderer declares the container it is served: mp4 for a
	// self-fetching remux (the HLS variant is pinned separately below), MPEG-TS for
	// the push-only spool (what runSpooled declares on its synthetic renderer).
	caps := func(selfFetch bool, containers ...string) media.Renderer {
		r := media.Renderer{SelfFetch: selfFetch, Containers: containers}
		if selfFetch {
			r.ServedContainer = media.MP4
		} else {
			r.ServedContainer = media.MPEGTS
		}
		return r
	}

	tests := []struct {
		name     string
		caps     media.Renderer
		sourceCT string
		whisper  bool
		delivery DeliveryMode
		subtitle SubtitleMode
		outputCT string
	}{
		{
			// Chromecast direct play: self-fetches and already takes the source
			// container, so the URL is handed straight to it, no local stream.
			name:     "chromecast passthrough when it self-fetches and accepts the container",
			caps:     caps(true, media.MP4),
			sourceCT: media.MP4,
			whisper:  false,
			delivery: DeliverPassthrough,
			subtitle: SubtitleOff,
			// OutputContentType is inert on pass-through (the device reads the
			// source's own type), so the plan leaves it empty.
			outputCT: "",
		},
		{
			// Burn-in cannot survive a pass-through: there is no local encode to
			// draw cues into, so even with the transcriber enabled the subtitle
			// axis is forced Off. This is the delivery-gating in NewPlan.
			name:     "passthrough forces subtitles off even with whisper enabled",
			caps:     caps(true, media.MP4),
			sourceCT: media.MP4,
			whisper:  true,
			delivery: DeliverPassthrough,
			subtitle: SubtitleOff,
			outputCT: "",
		},
		{
			// Chromecast remux: self-fetches but rejects the source container, so
			// castor serves a fragmented-mp4 remux the receiver decodes.
			name:     "chromecast remux to mp4 when it self-fetches but rejects the container",
			caps:     caps(true, media.MP4),
			sourceCT: media.MKV,
			whisper:  false,
			delivery: DeliverServe,
			subtitle: SubtitleOff,
			outputCT: media.MP4,
		},
		{
			// Chromecast remux with the transcriber enabled: it self-fetches, so it
			// takes captions as a native track (Stage 2), never burn-in. The plan
			// must not carry BurnIn the remux stage would silently ignore.
			name:     "chromecast remux leaves subtitles off even with whisper enabled",
			caps:     caps(true, media.MP4),
			sourceCT: media.MKV,
			whisper:  true,
			delivery: DeliverServe,
			subtitle: SubtitleOff,
			outputCT: media.MP4,
		},
		{
			// Roku remux: it self-fetches but rejects the source container and
			// advertises live HLS as its served container, so the plan targets HLS
			// rather than the default fragmented mp4. This is the ServedContainer axis.
			name:     "roku remux to hls when its served container is hls",
			caps:     media.Renderer{SelfFetch: true, Containers: []string{media.HLS, media.MP4, media.MKV}, ServedContainer: media.HLS},
			sourceCT: media.AVI,
			whisper:  false,
			delivery: DeliverServe,
			subtitle: SubtitleOff,
			outputCT: media.HLS,
		},
		{
			// DLNA never self-fetches, so it always serves, and its served
			// container is MPEG-TS regardless of the source container.
			name:     "dlna serves mpegts because it never self-fetches",
			caps:     caps(false),
			sourceCT: media.MKV,
			whisper:  false,
			delivery: DeliverServe,
			subtitle: SubtitleOff,
			outputCT: mpegtsContentType,
		},
		{
			// The AND in the delivery rule: a renderer that accepts the container
			// but does NOT self-fetch still serves (DLNA can only play what we
			// push it), so accepting a container is not on its own pass-through.
			name:     "accepting the container without self-fetch still serves",
			caps:     caps(false, media.MP4),
			sourceCT: media.MP4,
			whisper:  false,
			delivery: DeliverServe,
			subtitle: SubtitleOff,
			outputCT: mpegtsContentType,
		},
		{
			// Today's DLNA-only burn-in: a served cast with the transcriber on
			// draws the cues into the encode.
			name:     "served cast burns in subtitles when whisper is enabled",
			caps:     caps(false),
			sourceCT: media.MKV,
			whisper:  true,
			delivery: DeliverServe,
			subtitle: SubtitleBurnIn,
			outputCT: mpegtsContentType,
		},
		{
			// The same served cast with the transcriber off carries no subtitles.
			name:     "served cast leaves subtitles off when whisper is disabled",
			caps:     caps(false),
			sourceCT: media.MKV,
			whisper:  false,
			delivery: DeliverServe,
			subtitle: SubtitleOff,
			outputCT: mpegtsContentType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &media.Stream{ContentType: tt.sourceCT}
			cfg := Config{Whisper: subtitle.Whisper{Enable: tt.whisper}}

			plan := NewPlan(source, tt.caps, cfg)

			if plan.Delivery != tt.delivery {
				t.Errorf("Delivery = %s, want %s", deliveryName(plan.Delivery), deliveryName(tt.delivery))
			}
			if plan.Subtitle != tt.subtitle {
				t.Errorf("Subtitle = %s, want %s", subtitleName(plan.Subtitle), subtitleName(tt.subtitle))
			}
			if plan.OutputContentType != tt.outputCT {
				t.Errorf("OutputContentType = %q, want %q", plan.OutputContentType, tt.outputCT)
			}
		})
	}
}

func deliveryName(d DeliveryMode) string {
	switch d {
	case DeliverPassthrough:
		return "Passthrough"
	case DeliverServe:
		return "Serve"
	}
	return fmt.Sprintf("DeliveryMode(%d)", d)
}

func subtitleName(s SubtitleMode) string {
	switch s {
	case SubtitleOff:
		return "Off"
	case SubtitleBurnIn:
		return "BurnIn"
	}
	return fmt.Sprintf("SubtitleMode(%d)", s)
}
