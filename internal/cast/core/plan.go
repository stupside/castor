package core

import (
	"github.com/stupside/castor/internal/media"
)

// DeliveryPreference is what the operator asks of the delivery axis, as opposed
// to DeliveryMode, which is what the planner decided. It exists for the one case
// castor cannot infer: a source that lies about itself (a playlist whose segments
// are served under a disguised extension, say) is fetchable as far as castor can
// tell, yet the renderer refuses it. No evidence distinguishes that source from a
// well-formed one, so only the operator can say.
//
// Nothing else about a cast is configurable this way on purpose. Every other axis
// is inferred from evidence castor holds (a probe, advertised capabilities), and a
// renderer that misbehaves there is a capability-data fix, not a knob.
type DeliveryPreference string

const (
	// DeliveryAuto leaves the decision to ResolveDelivery. It is also what an
	// unset key means, so the zero value needs no default.
	DeliveryAuto DeliveryPreference = "auto"
	// DeliveryServe refuses pass-through for every cast: castor reads the source
	// and serves the renderer a local stream, whatever the source looks like.
	DeliveryServe DeliveryPreference = "serve"
)

// DeliveryMode is how the renderer receives the stream bytes: it either fetches
// the source URL itself (pass-through) or castor produces a stream locally and
// serves it. This is the first axis of a Plan; the copy-vs-encode and subtitle
// decisions only ever apply on a served cast, so several stages gate on it.
type DeliveryMode int

const (
	// DeliverPassthrough hands the device the source URL and lets it pull the
	// bytes directly: no local ffmpeg, no served stream. Chosen only when the
	// renderer self-fetches, the source needs nothing but its URL to be fetched,
	// AND the renderer already accepts the source container, so nothing needs
	// rewrapping.
	DeliverPassthrough DeliveryMode = iota
	// DeliverServe produces the stream locally (a remux or a transcode) and serves
	// it to the device. A push-only renderer always lands here (it never
	// self-fetches), as does a self-fetching renderer whose source container it
	// will not take or whose source only answers to the request headers castor
	// captured (a renderer is handed a URL, never those headers).
	DeliverServe
)

// SubtitleMode is the subtitle axis of a Plan. Stage 1 carries only the two modes
// today's code produces; a future SubtitleSidecar (a separately served caption
// track the device loads) is wired as a new enum value so the executor gains a
// branch without any reshuffle of the existing ones.
type SubtitleMode int

const (
	// SubtitleOff ships no subtitles: every pass-through cast, every served cast
	// with the transcriber disabled, and (this stage) every self-fetching renderer.
	SubtitleOff SubtitleMode = iota
	// SubtitleBurnIn transcribes the audio and draws the cues into the video
	// frames during the encode (whisper hardsubs). It forces a video re-encode
	// (drawtext needs decoded frames) and so is only reachable on a served cast.
	SubtitleBurnIn
)

// Plan is the pure decision record for one cast: what the executor must do,
// derived from (source, renderer capabilities, config) with no I/O. It is the
// single hand-off between the decision layer (this package) and the mechanism
// layer (the stages that pull, transcode, and serve).
//
// It fixes only the axes that need no probe. It deliberately carries no encode
// invocation: copy-vs-encode needs a ProbeInfo, and the probe that matters is
// unavailable at planning time. The served spool path probes its local spool (the
// source URL can be single-use) and the network remux path probes the source URL,
// each obtained only once its stage runs, so each served stage builds its own
// EncodeOptions from the right probe (ResolveVideo + ResolveAudio). Nothing about
// the encode lives on the Plan.
type Plan struct {
	// Delivery is pass-through versus locally served.
	Delivery DeliveryMode
	// Subtitle is off versus burned-in.
	Subtitle SubtitleMode
	// OutputContentType is the MIME type the device is told it is fetching: the
	// container the local ffmpeg muxes on a served cast, taken from the renderer's
	// declared ServedContainer. A pass-through cast reads the source's own content
	// type and never consults this, so it is left empty there.
	OutputContentType string
}

// NewPlan computes the pure Plan for a cast from the source, the renderer's
// advertised capabilities, and config. It performs no I/O and reads no probe: it
// fixes only the axes that need none (delivery, subtitle, and the served output
// container). Each served stage then completes the encode against the probe it
// obtains (the source URL for a network remux, the local spool for the read-once
// spool path), which is why nothing about the encode lives on the Plan.
func NewPlan(source *media.Stream, caps media.Renderer, cfg Config) Plan {
	delivery := ResolveDelivery(caps, source, cfg)
	return Plan{
		Delivery:          delivery,
		Subtitle:          ResolveSubtitle(caps, cfg),
		OutputContentType: outputContentType(delivery, caps),
	}
}

// outputContentType is the container the device is told it is fetching on a
// served cast: the renderer's declared ServedContainer, whichever delivery it
// reached Serve through. Core reads it straight from the capability and bakes in
// no container of its own, so no device family's format choice lives here. A
// pass-through cast never consults this (it reads the source's own content type),
// so its value is inert.
func outputContentType(delivery DeliveryMode, caps media.Renderer) string {
	if delivery == DeliverServe {
		return caps.ServedContainer
	}
	return ""
}
