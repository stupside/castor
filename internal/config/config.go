// Package config assembles the application configuration. It sits at the
// edge of the dependency graph: every section's type is owned by the package
// that consumes it (cast, extractor, resolve) and composed here, so domain
// packages never import application-level state.
package config

import (
	"strconv"
	"strings"

	"github.com/stupside/castor/internal/cast"
	"github.com/stupside/castor/internal/cast/core"
	"github.com/stupside/castor/internal/device"
	"github.com/stupside/castor/internal/source/extract"
	"github.com/stupside/castor/internal/source/resolve"
)

type Config struct {
	Device    DeviceConfig          `yaml:"device" validate:"required"`
	Network   cast.NetworkConfig    `yaml:"network" validate:"required"`
	Browser   extract.BrowserConfig `yaml:"browser" validate:"required"`
	Capture   extract.CaptureConfig `yaml:"capture" validate:"required"`
	Actions   extract.ActionConfig  `yaml:"actions" validate:"required"`
	Sources   []Source              `yaml:"sources" validate:"dive"`
	Resolver  resolve.Config        `yaml:"resolver" validate:"required"`
	Transcode cast.TranscodeConfig  `yaml:"transcode" validate:"required"`
	Whisper   cast.WhisperConfig    `yaml:"whisper"`
	TMDB      TMDB                  `yaml:"tmdb"`
}

// TMDB holds settings for the TMDB browse subcommand. The API key may also
// be supplied via the CASTOR_TMDB__API_KEY environment variable, so it is
// intentionally not marked required here.
type TMDB struct {
	APIKey string `yaml:"api_key"`
}

// DeviceConfig is the composition-root device section: the generic cast target
// plus each device family's optional connect settings. This is the one place the
// application binds a family's typed config (device.RokuConfig) to its YAML, so
// the agnostic layers below never name a family; resolve() collapses it into the
// opaque device.Config they carry.
type DeviceConfig struct {
	Name string      `yaml:"name" validate:"required"`
	Type device.Type `yaml:"type" validate:"required"`

	Roku device.RokuConfig `yaml:"roku"`
}

// resolve builds the agnostic device.Config, attaching the selected family's
// connect settings as the opaque Family payload the device layer interprets.
func (d DeviceConfig) resolve() device.Config {
	cfg := device.Config{Name: d.Name, Type: d.Type}
	switch d.Type {
	case device.TypeRoku:
		cfg.Family = d.Roku
	}
	return cfg
}

func (c *Config) Cast() cast.Config {
	return cast.Config{
		Config: core.Config{
			Device:    c.Device.resolve(),
			Network:   c.Network,
			Transcode: c.Transcode,
			Resolver:  c.Resolver,
			Whisper:   c.Whisper,
		},
	}
}

func (c *Config) Extractor() extract.Config {
	return extract.Config{
		Browser: c.Browser,
		Capture: c.Capture,
		Actions: c.Actions,
	}
}

// Source defines a set of proxy hosts and the URL templates to reach a movie
// or episode page on them.
type Source struct {
	Proxies   []string  `yaml:"proxies" validate:"required,min=1"`
	Templates Templates `yaml:"templates" validate:"required"`
}

func (c *Config) AllMovieURLs(itemID string) []string {
	var urls []string
	for _, s := range c.Sources {
		urls = append(urls, s.MovieURLs(itemID)...)
	}
	return urls
}

func (c *Config) AllEpisodeURLs(itemID string, season, episode uint) []string {
	var urls []string
	for _, s := range c.Sources {
		urls = append(urls, s.EpisodeURLs(itemID, season, episode)...)
	}
	return urls
}

type Templates struct {
	Movie   string `yaml:"movie" validate:"required"`
	Episode string `yaml:"episode" validate:"required"`
}

func (s *Source) MovieURLs(itemID string) []string {
	return s.expandTemplate(s.Templates.Movie, "{itemID}", itemID)
}

func (s *Source) EpisodeURLs(itemID string, season, episode uint) []string {
	return s.expandTemplate(s.Templates.Episode,
		"{itemID}", itemID,
		"{season}", strconv.FormatUint(uint64(season), 10),
		"{episode}", strconv.FormatUint(uint64(episode), 10),
	)
}

// expandTemplate substitutes placeholder/value pairs into tmpl and prefixes
// the result with every proxy host.
func (s *Source) expandTemplate(tmpl string, pairs ...string) []string {
	route := strings.NewReplacer(pairs...).Replace(tmpl)
	urls := make([]string, len(s.Proxies))
	for i, proxy := range s.Proxies {
		urls[i] = proxy + route
	}
	return urls
}
