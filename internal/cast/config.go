package cast

import (
	"github.com/stupside/castor/internal/cast/core"
	"github.com/stupside/castor/internal/cast/subtitle"
)

// Config is the application-facing cast configuration. It is just core.Config
// embedded, so cfg.Device etc. read through and cfg.Config is the exact value
// the shared machinery wants. The subtitle-transcription knob (Whisper) now
// lives on core.Config itself, because the pure planner reads it to choose the
// subtitle axis and must reach it with no import of any renderer; there is no
// separate Whisper field here to shadow it.
type Config struct {
	core.Config
}

// Every config section's type is re-exported here so the application composes
// the cast surface through this one package (cast.NetworkConfig, cast.WhisperConfig,
// ...) instead of reaching into core or the subtitle building block. The
// definitions live in the packages that consume them. The device section is the
// exception: the composition root (internal/config) owns its own device config so
// it can bind a family's connect settings, so no device alias is re-exported here.
type (
	NetworkConfig   = core.NetworkConfig
	TranscodeConfig = core.TranscodeConfig
	WhisperConfig   = subtitle.Whisper
)
