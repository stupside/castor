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
	Cast      CastConfig            `yaml:"cast"`
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

// CastConfig is the cast-behaviour section: the decisions castor cannot infer
// and so leaves to the operator. It holds exactly one axis today, and the bar for
// a second is high — every other decision the pipeline makes is derived from a
// probe or from advertised capabilities, where a knob would bury a bug rather
// than fix it.
type CastConfig struct {
	// Delivery forces castor to relay the stream ("serve") instead of deciding
	// per source ("auto", the default). Set it for a source that a renderer
	// refuses even though nothing about it looks unfetchable, e.g. an HLS
	// playlist whose segments are served under a disguised extension. It costs
	// this machine's bandwidth and CPU on every cast, which is why it is opt-in.
	Delivery core.DeliveryPreference `yaml:"delivery" validate:"omitempty,oneof=auto serve"`
}

// DeviceConfig is the composition-root device section: the generic cast target
// plus each device family's optional connect settings. This is the one place the
// application binds a family's typed config (device.RokuConfig) to its YAML, so
// the agnostic layers below never name a family; resolve() collapses it into the
// opaque device.Config they carry.
type DeviceConfig struct {
	// Name identifies the device to discovery. It is required only without Host:
	// once Host pins the device, Name is just a display label and may be omitted.
	Name string      `yaml:"name" validate:"required_without=Host"`
	Type device.Type `yaml:"type" validate:"required"`

	// Host pins the device's address to skip discovery (see device.Config.Address):
	// a bare IP/host for any family, or a DLNA description URL. Left empty, Castor
	// discovers the device by Name over SSDP/mDNS.
	Host string `yaml:"host"`

	Roku device.RokuConfig `yaml:"roku"`
}

// resolve builds the agnostic device.Config, attaching the selected family's
// connect settings as the opaque Family payload the device layer interprets.
func (d DeviceConfig) resolve() device.Config {
	cfg := device.Config{Name: d.Name, Type: d.Type, Address: d.Host}
	switch d.Type {
	case device.TypeRoku:
		cfg.Family = d.Roku
	}
	return cfg
}

// Playback assembles the configuration a cast runs on, collapsing this root's
// sections into the domain types the cast layer consumes.
func (c *Config) Playback() cast.Config {
	return cast.Config{
		Config: core.Config{
			Device:    c.Device.resolve(),
			Network:   c.Network,
			Transcode: c.Transcode,
			Resolver:  c.Resolver,
			Whisper:   c.Whisper,
			Delivery:  c.Cast.Delivery,
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
