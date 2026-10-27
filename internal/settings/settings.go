// Package settings is how a castor command reads its configuration: the user's file, the working directory's, then the one named, each with its .local overlay, then CASTOR_ variables, each command decoding only the sections it reads.
package settings

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
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
var Flag cli.Flag = &cli.StringFlag{Name: configFlag, Aliases: []string{"c"}, Usage: "Configuration file merged over castor/config.yaml in the user config directory and config.yaml in the working directory"}

const fileName = "config.yaml"

// Load reads the configuration cmd names; the default files may be absent (environment and defaults suffice), a path the user named may not.
func Load[T any](cmd *cli.Command, defaults T) (*T, error) {
	var paths []string
	if dir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "castor", fileName))
	}
	paths = append(paths, fileName)
	if cmd.IsSet(configFlag) {
		path := cmd.String(configFlag)
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("config file: %w", err)
		}
		paths = append(paths, path)
	}
	return read(paths, defaults)
}

// read decodes over defaults the sections T declares, from each file at paths that exists, each followed by its .local overlay, then the environment, and validates them.
func read[T any](paths []string, defaults T) (*T, error) {
	k := koanf.New(".")
	for _, path := range paths {
		local := strings.TrimSuffix(path, filepath.Ext(path)) + ".local" + filepath.Ext(path)
		for _, layer := range []string{path, local} {
			if _, err := os.Stat(layer); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return nil, fmt.Errorf("reading %s: %w", layer, err)
			}
			if err := k.Load(file.Provider(layer), yaml.Parser()); err != nil {
				return nil, fmt.Errorf("loading %s: %w", layer, err)
			}
		}
	}
	if err := k.Load(env.Provider(envPrefix, ".", envKey), nil); err != nil {
		return nil, fmt.Errorf("loading environment overrides: %w", err)
	}
	if err := checkOwnedSections(k, reflect.TypeFor[T](), ""); err != nil {
		return nil, err
	}

	cfg := &defaults
	if err := unmarshal(k, "", cfg, false); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}
	if err := validator.New().Struct(cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}
	return cfg, nil
}

// checkOwnedSections uses the config type's settings:"strict" tags to reject
// unknown keys only in sections that this command owns. Other commands may have
// additional fields in the same configuration file.
func checkOwnedSections(k *koanf.Koanf, typ reflect.Type, parent string) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return nil
	}
	for field := range typ.Fields() {
		if !field.IsExported() {
			continue
		}
		name, options, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "-" {
			continue
		}
		if name == "" && options == "inline" {
			if err := checkOwnedSections(k, field.Type, parent); err != nil {
				return err
			}
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		path := name
		if parent != "" {
			path = parent + "." + name
		}
		if field.Tag.Get("settings") == "strict" {
			if !k.Exists(path) {
				continue
			}
			destination := reflect.New(field.Type).Interface()
			if err := unmarshal(k, path, destination, true); err != nil {
				return fmt.Errorf("checking %s: %w", path, err)
			}
			continue
		}
		if err := checkOwnedSections(k, field.Type, path); err != nil {
			return err
		}
	}
	return nil
}

func unmarshal(k *koanf.Koanf, path string, destination any, strict bool) error {
	return k.UnmarshalWithConf(path, destination, koanf.UnmarshalConf{
		Tag: "yaml",
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
				mapstructure.StringToSliceHookFunc(","),
				mapstructure.TextUnmarshallerHookFunc(),
			),
			WeaklyTypedInput: true,
			SquashTagOption:  "inline",
			ErrorUnused:      strict,
		},
	})
}

// envKey maps CASTOR_SECTION__FIELD to the koanf key section.field.
func envKey(s string) string {
	s = strings.TrimPrefix(s, envPrefix)
	s = strings.ToLower(s)
	return strings.ReplaceAll(s, "__", ".")
}
