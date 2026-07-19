package resolve

import "time"

type Config struct {
	HLSTimeout          time.Duration `yaml:"hls_timeout" validate:"required"`
	FFprobePath         string        `yaml:"ffprobe_path" validate:"required"`
	ProbeTimeout        time.Duration `yaml:"probe_timeout" validate:"required"`
	ProbeMaxConcurrency int           `yaml:"probe_max_concurrency" validate:"required,min=1"`

	// MaxHeight is the tallest video the user wants cast: source selection
	// prefers the largest HLS variant no taller than this, and the encoder
	// scales its output down to it. Set it to your renderer's native height
	// (e.g. 2160 for a 4K TV). Required, so it is always an explicit ceiling.
	MaxHeight int `yaml:"max_height" validate:"required,min=1"`
}
