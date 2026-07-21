package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"

	"github.com/stupside/castor/internal/cast"
	"github.com/stupside/castor/internal/cast/whisper"
	"github.com/stupside/castor/internal/source/extract"
	"github.com/stupside/castor/internal/source/resolve"
)

// defaults is the base configuration layer: every knob a fresh install
// shouldn't have to care about. It's the real, typed Config so a default is
// checked against the field it fills (a mistyped key can't compile), and it's
// loaded as the lowest-priority layer: the file and environment overlay it,
// so an explicit value always wins. Only what genuinely identifies an install
// is left zero: the device to cast to and the sources to cast from.
func defaults() *Config {
	return &Config{
		Network: cast.NetworkConfig{Timeout: 5 * time.Second},
		Browser: extract.BrowserConfig{Timeout: 30 * time.Second, Headless: true},
		Resolver: resolve.Config{
			HLSTimeout:          30 * time.Second,
			FFprobePath:         "ffprobe",
			ProbeTimeout:        30 * time.Second,
			ProbeMaxConcurrency: 2,
			MaxHeight:           1080,
		},
		Capture: extract.CaptureConfig{
			Patterns:          []string{`\.m3u8`, `master\.m3u8`, `index\.m3u8`, `/playlist/`},
			MaxCandidates:     100,
			MaxConcurrency:    4,
			CollectionWindow:  10 * time.Second,
			GraceAfterActions: 15 * time.Second,
		},
		Actions: extract.ActionConfig{
			NavigateIframeTimeout:  10 * time.Second,
			NavigateIframeMaxDepth: 5,
			TurnstileRetryTimeout:  10 * time.Second,
			BypassTurnstileTimeout: 20 * time.Second,
		},
		Transcode: cast.TranscodeConfig{FFmpegPath: "ffmpeg", RWTimeout: 30 * time.Second},
		// Pinned rather than "auto": the streaming transcriber re-detects on
		// every buffer with auto, which misfires on music and quiet stretches.
		Whisper: whisper.Config{Language: "en"},
	}
}

// envPrefix is the prefix for environment overrides. Convention:
// CASTOR_SECTION__FIELD — the double underscore separates the section from
// the field so single underscores can stay in field names.
//
//	CASTOR_TMDB__API_KEY       → tmdb.api_key
//	CASTOR_NETWORK__TIMEOUT    → network.timeout
//	CASTOR_BROWSER__NO_SANDBOX → browser.no_sandbox
const envPrefix = "CASTOR_"

// Load reads the YAML file at path plus a sibling *.local.yaml and CASTOR_*
// environment variables, decodes them over the typed defaults, and validates
// the result.
func Load(path string) (*Config, error) {
	k := koanf.New(".")

	if _, err := os.Stat(path); err == nil {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("loading %s: %w", path, err)
		}
	}
	// A sibling *.local.yaml overlays the tracked config with personal
	// values (API keys, device names) that must stay out of version
	// control; .gitignore covers config.local.yaml.
	local := strings.TrimSuffix(path, filepath.Ext(path)) + ".local" + filepath.Ext(path)
	if _, err := os.Stat(local); err == nil {
		if err := k.Load(file.Provider(local), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("loading %s: %w", local, err)
		}
	}
	if err := k.Load(env.Provider(envPrefix, ".", envKey), nil); err != nil {
		return nil, fmt.Errorf("loading environment overrides: %w", err)
	}

	// Seed the target with the defaults and let the file and environment layers
	// overwrite only the keys they actually carry. mapstructure leaves a field
	// untouched when its key is absent or null, so a present-but-empty section
	// (`resolver:` with everything commented out is YAML null) keeps every
	// default beneath it, and a partial section overrides just the keys it names.
	cfg := defaults()
	if err := k.UnmarshalWithConf("", cfg, koanf.UnmarshalConf{
		Tag: "yaml",
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
				mapstructure.StringToSliceHookFunc(","),
				mapstructure.TextUnmarshallerHookFunc(),
			),
			WeaklyTypedInput: true,
		},
	}); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	if err := validator.New().Struct(cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}
	return cfg, nil
}

// envKey maps CASTOR_SECTION__FIELD to the koanf key section.field.
func envKey(s string) string {
	s = strings.TrimPrefix(s, envPrefix)
	s = strings.ToLower(s)
	return strings.ReplaceAll(s, "__", ".")
}
