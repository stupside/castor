package source

import (
	"context"
	"log/slog"
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// Reader reads the links of one shape.
type Reader interface {
	Resolve(ctx context.Context, env Env, s Subject) (Resolution, error)

	// Timeline follows an input whose timeline castor keeps, read for the kind of track given; nil for one ffmpeg reads directly.
	Timeline(c Client, in media.Input, reads media.TrackKind) timeline.Source

	// InputArgs is how ffmpeg and ffprobe open an input of this shape; nil when they need no telling.
	InputArgs(segmentRetries int) []string
}

// Format is a Reader for a document grammar, which links announce and bodies show.
type Format interface {
	Reader

	// Identity is the content type this format reads and the names a link announces it under.
	Identity() Identity

	// Recognize reports whether a body carries this format's signature, even when a sniffed head is incomplete.
	Recognize(body string) bool
}

// Identity is how a link says it carries one content type: a file extension, or a server-confirmed MIME type.
type Identity struct {
	ContentType string
	Extensions  []string
	MIMETypes   []string
}

// Env is what every format resolves with: the origin's client and the cast's height ceiling.
type Env struct {
	Client    Client
	MaxHeight media.HeightCap
}

// Choose is the rung a cast reads from origin, and says so: the one settled on earlier while the manifest still names it, else the best under the cast's ceiling by preferred.
func (e Env) Choose(ctx context.Context, origin Origin, settled Rendition, preferred func(a, b Rendition) int) Rendition {
	chosen := origin.Choose(e.MaxHeight, preferred)
	if i := slices.IndexFunc(origin.Renditions, func(r Rendition) bool {
		if settled.Representation != "" {
			return r.Representation == settled.Representation
		}
		return settled.URL != nil && r.URL != nil && r.URL.String() == settled.URL.String()
	}); i >= 0 {
		chosen = origin.Renditions[i]
	}
	level, msg := slog.LevelInfo, "rendition selected"
	if !e.MaxHeight.Admits(chosen.Height) {
		level, msg = slog.LevelWarn, "no rendition under the height cap; reading the shortest on offer and scaling it down"
	}
	slog.Log(ctx, level, msg,
		"height", chosen.Height, "cap", int(e.MaxHeight), "declared_bitrate", int64(chosen.Bitrate),
		"representation", chosen.Representation,
		"renditions", len(origin.Renditions), "sole", origin.Sole())
	return chosen
}

// Subject is the link a format resolves, what is known of its origin so far, and the rung already settled on.
type Subject struct {
	Stream Stream
	Origin Origin
	Chosen Rendition
}
