package cast

import (
	"time"

	"github.com/stupside/castor/internal/cast/whisper"
	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/source/resolve"
)

// Config is everything Play needs. The application config composes these
// types; cast never reads app-level state.
type Config struct {
	Device    DeviceConfig
	Network   NetworkConfig
	Transcode TranscodeConfig
	Whisper   whisper.Config
	Resolver  resolve.Config
}

type DeviceConfig struct {
	Name string      `yaml:"name" validate:"required"`
	Type device.Type `yaml:"type" validate:"required"`
}

type NetworkConfig struct {
	Timeout   time.Duration `yaml:"timeout" validate:"required"`
	Interface string        `yaml:"interface"`
}

// TranscodeConfig holds the small set of ffmpeg settings that aren't decided
// by the planner. Codec/bitrate/format choices live in the per-device plan;
// only the binary path and the upstream I/O timeout come from config.
type TranscodeConfig struct {
	FFmpegPath string        `yaml:"ffmpeg_path" validate:"required"`
	RWTimeout  time.Duration `yaml:"rw_timeout" validate:"required"`
}
