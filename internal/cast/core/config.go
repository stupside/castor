// Package core holds the device-agnostic decision layer and the machinery every
// cast shares: config, source resolution, device discovery/connect, the pure
// Plan (delivery/subtitle/output axes) with its copy-vs-encode resolvers, and the
// replay-server delivery. It knows nothing about any specific device family; the
// pipeline executor and the device adapters import core, never the other way
// round, so a device concern physically cannot leak across the boundary or into
// this core.
package core

import (
	"time"

	"github.com/stupside/castor/internal/cast/subtitle"
	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/source/resolve"
)

// Config is the device-neutral configuration the planner and every stage share:
// the target device, network, transcode binary, source resolver, and the
// subtitle transcriber. It composes only domain types, so core imports no
// application state; the app config assembles this and hands it in.
type Config struct {
	Device    DeviceConfig
	Network   NetworkConfig
	Transcode TranscodeConfig
	Resolver  resolve.Config

	// Whisper is the subtitle-transcription knob NewPlan reads to choose the
	// subtitle axis (Enable gates burn-in). Its type lives in the cgo-free
	// subtitle package, not the whisper transcriber, so this decision core carries
	// it without importing whisper's cgo. A device that never serves (or a disabled
	// transcriber) simply resolves to SubtitleOff.
	Whisper subtitle.Whisper
}

// DeviceConfig is the device section, owned by the device package so this
// device-agnostic layer neither defines nor names any device family. Core reads
// only its generic Name/Type (for discovery) and forwards the whole value to
// device.Connect, which alone interprets any family-specific field.
type DeviceConfig = device.Config

type NetworkConfig struct {
	Timeout   time.Duration `yaml:"timeout" validate:"required"`
	Interface string        `yaml:"interface"`
}

// TranscodeConfig holds the small set of ffmpeg settings that aren't decided by
// the plan. Codec/bitrate/format choices are resolved from capabilities (see the
// Resolve* functions); only the binary path and the upstream I/O timeout, which
// no capability can determine, come from config.
type TranscodeConfig struct {
	FFmpegPath string        `yaml:"ffmpeg_path" validate:"required"`
	RWTimeout  time.Duration `yaml:"rw_timeout" validate:"required"`
}
