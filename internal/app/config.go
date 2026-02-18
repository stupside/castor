package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/device"
)

// Config holds all application configuration.
type Config struct {
	Device    DeviceConfig    `koanf:"device" validate:"required"`
	Network   NetworkConfig   `koanf:"network" validate:"required"`
	Browser   BrowserConfig   `koanf:"browser" validate:"required"`
	Resolver  ResolverConfig  `koanf:"resolver" validate:"required"`
	Capture   CaptureConfig   `koanf:"capture" validate:"required"`
	Actions   ActionConfig    `koanf:"actions" validate:"required"`
	Sources   []SourceConfig  `koanf:"sources" validate:"dive"`
	Transcode TranscodeConfig `koanf:"transcode" validate:"required"`
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
	FFprobePath         string        `koanf:"ffprobe_path" validate:"required"`
	ProbeTimeout        time.Duration `koanf:"probe_timeout" validate:"required"`
	HLSTimeout          time.Duration `koanf:"hls_timeout" validate:"required"`
	ProbeMaxConcurrency int           `koanf:"probe_max_concurrency" validate:"required,min=1"`
}

// BrowserConfig holds settings for headless browser extraction.
type BrowserConfig struct {
	Timeout    time.Duration `koanf:"timeout" validate:"required"`
	Headless   bool          `koanf:"headless"`
	NoSandbox  bool          `koanf:"no_sandbox"`
	ChromePath string        `koanf:"chrome_path"`
}

// SourceConfig defines a YAML-configured source.
type SourceConfig struct {
	Name      string         `koanf:"name" validate:"required"`
	Proxies   []string       `koanf:"proxies" validate:"required,min=1"`
	Templates TemplateConfig `koanf:"templates" validate:"required"`
}

// TemplateConfig holds URL templates for movies and episodes.
type TemplateConfig struct {
	Movie   string `koanf:"movie" validate:"required"`
	Episode string `koanf:"episode" validate:"required"`
}

// TranscodeConfig holds ffmpeg transcode settings.
type TranscodeConfig struct {
	FFmpegPath           string `koanf:"ffmpeg_path" validate:"required"`
	ReadRate             int    `koanf:"read_rate" validate:"required"`
	ReadRateBurst        int    `koanf:"read_rate_burst" validate:"required"`
	VideoCodec           string `koanf:"video_codec" validate:"required"`
	AudioCodec           string `koanf:"audio_codec" validate:"required"`
	AudioSampleRate      int    `koanf:"audio_sample_rate" validate:"required"`
	AudioBitrate         string `koanf:"audio_bitrate" validate:"required"`
	OutputFormat         string `koanf:"output_format" validate:"required"`
	InitialDataThreshold int    `koanf:"initial_data_threshold" validate:"required"`
}

// CaptureConfig holds patterns for intercepting stream URLs.
type CaptureConfig struct {
	Patterns          []string      `koanf:"patterns" validate:"required,min=1"`
	CollectionWindow  time.Duration `koanf:"collection_window" validate:"required"`
	GraceAfterActions time.Duration `koanf:"grace_after_actions" validate:"required"`
	MaxConcurrency    int           `koanf:"max_concurrency" validate:"required,min=1"`
	MaxCandidates     int           `koanf:"max_candidates" validate:"required,min=1"`
}

// ActionConfig holds timeouts for browser automation actions.
type ActionConfig struct {
	NavigateIframeTimeout  time.Duration `koanf:"navigate_iframe_timeout" validate:"required"`
	NavigateIframeMaxDepth int           `koanf:"navigate_iframe_max_depth" validate:"required,min=1"`
	BypassTurnstileTimeout time.Duration `koanf:"bypass_turnstile_timeout" validate:"required"`
	TurnstileRetryTimeout  time.Duration `koanf:"turnstile_retry_timeout" validate:"required"`
}

// MovieURLs expands the movie template across all proxies for a source.
func (s *SourceConfig) MovieURLs(itemID string) []string {
	return s.expandTemplate(s.Templates.Movie, map[string]string{
		"{itemID}": itemID,
	})
}

// EpisodeURLs expands the episode template across all proxies for a source.
func (s *SourceConfig) EpisodeURLs(itemID string, season, episode uint) []string {
	return s.expandTemplate(s.Templates.Episode, map[string]string{
		"{itemID}":  itemID,
		"{season}":  fmt.Sprintf("%d", season),
		"{episode}": fmt.Sprintf("%d", episode),
	})
}

func (s *SourceConfig) expandTemplate(tmpl string, replacements map[string]string) []string {
	route := tmpl
	for placeholder, value := range replacements {
		route = strings.ReplaceAll(route, placeholder, value)
	}
	urls := make([]string, len(s.Proxies))
	for i, proxy := range s.Proxies {
		urls[i] = proxy + route
	}
	return urls
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

// Source returns the SourceConfig with the given name, or an error if not found.
func (c *Config) Source(name string) (*SourceConfig, error) {
	for i := range c.Sources {
		if c.Sources[i].Name == name {
			return &c.Sources[i], nil
		}
	}
	return nil, fmt.Errorf("source %q not found", name)
}
