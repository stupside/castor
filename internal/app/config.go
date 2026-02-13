package app

import (
	"fmt"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/transcode"
)

// Config holds all application configuration.
type Config struct {
	Device    DeviceConfig              `koanf:"device" validate:"required"`
	Network   NetworkConfig             `koanf:"network" validate:"required"`
	Browser   BrowserConfig             `koanf:"browser" validate:"required"`
	Resolver  ResolverConfig            `koanf:"resolver" validate:"required"`
	Scrapers  []ScraperConfig           `koanf:"scrapers" validate:"required,dive"`
	Transcode transcode.TranscodeConfig `koanf:"transcode" validate:"required"`
}

// DeviceConfig holds device selection settings.
type DeviceConfig struct {
	Name string      `koanf:"name" validate:"required"`
	Type device.Type `koanf:"type" validate:"required"`
}

// NetworkConfig holds network settings.
type NetworkConfig struct {
	Timeout   time.Duration `koanf:"timeout" validate:"required"`
	Interface string        `koanf:"interface" validate:"required"`
}

// ResolverConfig holds URL resolution settings.
type ResolverConfig struct {
	FFmpegPath  string `koanf:"ffmpeg_path" validate:"required"`
	FFprobePath string `koanf:"ffprobe_path" validate:"required"`
}

// BrowserConfig holds settings for headless browser extraction.
type BrowserConfig struct {
	Timeout    time.Duration `koanf:"timeout" validate:"required"`
	Headless   bool          `koanf:"headless"`
	NoSandbox  bool          `koanf:"no_sandbox"`
	ChromePath string        `koanf:"chrome_path" validate:"required"`
}

// ScraperConfig defines a YAML-configured scraper.
type ScraperConfig struct {
	Name      string         `koanf:"name" validate:"required"`
	Proxies   []string       `koanf:"proxies" validate:"required,min=1"`
	Capture   CaptureConfig  `koanf:"capture" validate:"required"`
	Templates TemplateConfig `koanf:"templates" validate:"required"`
}

// TemplateConfig holds URL templates for movies and episodes.
type TemplateConfig struct {
	Movie   string `koanf:"movie" validate:"required"`
	Episode string `koanf:"episode" validate:"required"`
}

// CaptureConfig holds patterns for intercepting stream URLs.
type CaptureConfig struct {
	Extensions       []string      `koanf:"extensions" validate:"required"`
	Substrings       []string      `koanf:"substrings" validate:"required"`
	CollectionWindow time.Duration `koanf:"collection_window" validate:"required"`
}

// Load reads and validates configuration from a YAML file.
func Load(path string) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("loading config from %s: %w", path, err)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	if err := validator.New().Struct(&cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// ConfigFrom extracts the Config from the CLI command metadata.
func ConfigFrom(cmd *cli.Command) (*Config, error) {
	v, ok := cmd.Root().Metadata["config"]
	if !ok {
		return nil, fmt.Errorf("config not found in command metadata")
	}
	cfg, ok := v.(*Config)
	if !ok {
		return nil, fmt.Errorf("config has unexpected type %T", v)
	}
	return cfg, nil
}
