// Package cast turns a resolved media stream into playback on a renderer.
//
// It is device-agnostic and carries no device-type switch. Play resolves the
// source (renderer-independent) and hands core.Connect plus the resolved stream
// to the pipeline executor, which connects the renderer, computes a pure
// core.Plan from its advertised capabilities, and runs the stages the plan names.
// Every device concern lives either in the device adapter (internal/device) or is
// expressed as capability data the plan reads, so no device family is a distinct
// code path here.
package cast

import (
	"context"

	"github.com/stupside/castor/internal/cast/core"
	"github.com/stupside/castor/internal/cast/pipeline"
	"github.com/stupside/castor/internal/media"
)

// Play resolves a stream and casts it to the configured device. Source resolution
// is renderer-independent and happens here; connecting the renderer is left to the
// pipeline, which times it against the delivery path: a non-self-fetching renderer
// connects concurrently with the pull, so slow discovery does not age a short-lived
// signed URL before the first byte. There is deliberately no device-type branch:
// adding a renderer family is a device adapter plus a set of capability values.
func Play(ctx context.Context, cfg Config, stream *media.Stream) error {
	resolved, localIP, err := core.ResolveSource(ctx, cfg.Config, stream)
	if err != nil {
		return err
	}
	return pipeline.Run(ctx, cfg.Config, core.Connect, resolved, localIP)
}
