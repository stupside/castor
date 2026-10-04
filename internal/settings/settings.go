// Package settings is how a castor command reads its configuration: one file, its .local overlay, then CASTOR_ variables, each command decoding only the sections it reads.
package settings

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/urfave/cli/v3"
)

const (
	configFlag = "config"
	envPrefix  = "CASTOR_"
)

// Flag is the root flag every command reads its configuration through.
var Flag cli.Flag = &cli.StringFlag{Name: configFlag, Aliases: []string{"c"}, Usage: "Path to configuration file", Value: "config.yaml"}

// Load reads the configuration cmd names; the default path may be absent (environment and defaults suffice), a path the user named may not.
func Load[T any](cmd *cli.Command, defaults T) (*T, error) {
	path := cmd.String(configFlag)
	if cmd.IsSet(configFlag) {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("config file: %w", err)
		}
	}
	return Read(path, defaults)
}

// Read decodes over defaults the sections T declares, from the file at path when there is one, its .local overlay and the environment, then validates them.
func Read[T any](path string, defaults T) (*T, error) {
	k := koanf.New(".")
	local := strings.TrimSuffix(path, filepath.Ext(path)) + ".local" + filepath.Ext(path)
	for _, layer := range []string{path, local} {
		if _, err := os.Stat(layer); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading %s: %w", layer, err)
		}
		if err := k.Load(file.Provider(layer), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("loading %s: %w", layer, err)
		}
	}
	if err := k.Load(env.Provider(envPrefix, ".", envKey), nil); err != nil {
		return nil, fmt.Errorf("loading environment overrides: %w", err)
	}

	cfg := &defaults
	if err := k.UnmarshalWithConf("", cfg, koanf.UnmarshalConf{
		Tag: "yaml",
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
				mapstructure.StringToSliceHookFunc(","),
				mapstructure.TextUnmarshallerHookFunc(),
			),
			WeaklyTypedInput: true,
			SquashTagOption:  "inline",
		},
	}); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}
	if err := validator.New().Struct(cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}
	slog.Debug("config loaded", "path", path)
	return cfg, nil
}

// envKey maps CASTOR_SECTION__FIELD to the koanf key section.field.
func envKey(s string) string {
	s = strings.TrimPrefix(s, envPrefix)
	s = strings.ToLower(s)
	return strings.ReplaceAll(s, "__", ".")
}
