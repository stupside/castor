package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// defaults is the base configuration layer: every knob a fresh install
// shouldn't have to care about. The file and environment overlay it, so an
// explicit value always wins. Only what genuinely identifies an install has
// no default — the device to cast to and the sources to cast from.
var defaults = map[string]any{
	"network.timeout": "5s",

	"browser.timeout":  "30s",
	"browser.headless": true,

	"resolver.hls_timeout":           "30s",
	"resolver.ffprobe_path":          "ffprobe",
	"resolver.probe_timeout":         "30s",
	"resolver.probe_max_concurrency": 8,

	"capture.patterns":            []string{`\.m3u8`, `master\.m3u8`, `index\.m3u8`, `/playlist/`},
	"capture.max_candidates":      100,
	"capture.max_concurrency":     4,
	"capture.collection_window":   "10s",
	"capture.grace_after_actions": "15s",

	"actions.navigate_iframe_timeout":   "10s",
	"actions.navigate_iframe_max_depth": 5,
	"actions.turnstile_retry_timeout":   "10s",
	"actions.bypass_turnstile_timeout":  "20s",

	"transcode.ffmpeg_path": "ffmpeg",
	"transcode.rw_timeout":  "30s",

	// Pinned rather than "auto": the streaming transcriber re-detects on
	// every buffer with auto, which misfires on music and quiet stretches.
	"whisper.language": "en",
}

// envPrefix is the prefix for environment overrides. Convention:
// CASTOR_SECTION__FIELD — the double underscore separates the section from
// the field so single underscores can stay in field names.
//
//	CASTOR_TMDB__API_KEY       → tmdb.api_key
//	CASTOR_NETWORK__TIMEOUT    → network.timeout
//	CASTOR_BROWSER__NO_SANDBOX → browser.no_sandbox
const envPrefix = "CASTOR_"

// Load layers defaults, the YAML file at path, and CASTOR_* environment
// variables (in that order — later wins), then validates the result.
func Load(path string) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(confmap.Provider(defaults, "."), nil); err != nil {
		return nil, fmt.Errorf("loading defaults: %w", err)
	}
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("loading %s: %w", path, err)
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

	cfg := new(Config)
	if err := k.UnmarshalWithConf("", cfg, koanf.UnmarshalConf{
		Tag: "yaml",
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				// Parse strings like "30s", "5m" into time.Duration.
				mapstructure.StringToTimeDurationHookFunc(),
				// Split comma-separated env strings into []string.
				mapstructure.StringToSliceHookFunc(","),
				// Honor custom types implementing encoding.TextUnmarshaler.
				mapstructure.TextUnmarshallerHookFunc(),
			),
			WeaklyTypedInput: true,
			Result:           cfg,
			TagName:          "yaml",
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
