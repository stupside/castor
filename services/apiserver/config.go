package apiserver

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/settings"
	"github.com/stupside/castor/services/apiserver/internal/device/roku"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Config is the API server's sections of castor's configuration.
type Config struct {
	Cast    CastConfig    `yaml:"cast" settings:"strict"`
	Servers ServersConfig `yaml:"servers" validate:"required"`
}

type ServersConfig struct {
	API   APIConfig    `yaml:"api" settings:"strict" validate:"required"`
	Media ServerConfig `yaml:"media"`
	// Scraping names only the resolver endpoint and credentials, never browser settings.
	Scraping ServerConfig `yaml:"scraping"`
}

// CastConfig is what every cast asks unless its request says otherwise; New holds it to the contract's rules.
type CastConfig struct {
	Delivery  Delivery `yaml:"delivery"`
	MaxHeight uint32   `yaml:"max_height"`
	// MaxBitrate is the most a cast asks of a device's link; absent asks nothing.
	MaxBitrate Bitrate `yaml:"max_bitrate"`
	// Subtitles selects the source's captions and optionally enables transcription as a fallback.
	Subtitles SubtitleConfig `yaml:"subtitles"`
	// Audio is the ordered list of languages to try before the source's default.
	Audio AudioConfig `yaml:"audio"`
}

// AudioConfig accepts a single legacy language or an ordered list.
type AudioConfig struct {
	Languages []string `yaml:"languages"`
}

func (c *AudioConfig) UnmarshalText(value []byte) error {
	*c = AudioConfig{}
	if len(value) > 0 {
		c.Languages = []string{string(value)}
	}
	return nil
}

// SubtitleConfig accepts one scalar language or an ordered mapping.
type SubtitleConfig struct {
	Languages           []string `yaml:"languages"`
	TranscribeIfMissing bool     `yaml:"transcribe_if_missing"`
}

func (c *SubtitleConfig) UnmarshalText(value []byte) error {
	*c = SubtitleConfig{}
	if string(value) == "auto" {
		return fmt.Errorf("subtitle selection %q is unsupported; choose preferred languages", value)
	}
	if len(value) > 0 {
		c.Languages = []string{string(value)}
	}
	return nil
}

// Delivery is how a cast reaches the device, spelled in configuration as the contract names it, such as auto.
type Delivery castorv1.Delivery

func (d *Delivery) UnmarshalText(text []byte) error {
	v, ok := castorv1.Delivery_value["DELIVERY_"+strings.ToUpper(string(text))]
	if !ok {
		return fmt.Errorf("unknown delivery %q", text)
	}
	*d = Delivery(v)
	return nil
}

// Bitrate is a rate in configuration: bits per second, optionally with a k or M suffix such as 6M.
type Bitrate uint64

func (b *Bitrate) UnmarshalText(text []byte) error {
	digits, unit := strings.TrimSpace(string(text)), uint64(1)
	switch {
	case strings.HasSuffix(digits, "k"):
		digits, unit = strings.TrimSuffix(digits, "k"), 1_000
	case strings.HasSuffix(digits, "M"):
		digits, unit = strings.TrimSuffix(digits, "M"), 1_000_000
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || n > math.MaxInt64/unit {
		return fmt.Errorf("unknown bitrate %q: want bits per second, such as 6000000, 6000k or 6M", text)
	}
	*b = Bitrate(n * unit)
	return nil
}

// APIConfig is where `castor api-server` listens, and the token it asks every request to carry.
type APIConfig struct {
	settings.Endpoint `yaml:",inline"`
	Listen            string        `yaml:"listen" validate:"required,hostname_port|startswith=:"`
	Network           NetworkConfig `yaml:"network" validate:"required"`
	Devices           DevicesConfig `yaml:"devices"`
}

// ServerConfig is the media or scraping service this API server reaches.
type ServerConfig = settings.Endpoint

// NetworkConfig is how long discovery and a device protocol are given.
type NetworkConfig struct {
	Timeout time.Duration `yaml:"timeout" validate:"required"`
}

// DevicesConfig is each device family's own settings.
type DevicesConfig struct {
	Roku roku.Config `yaml:"roku"`
}

func defaults() Config {
	return Config{
		Cast:    CastConfig{Delivery: Delivery(castorv1.Delivery_DELIVERY_AUTO), MaxHeight: 1080},
		Servers: ServersConfig{API: APIConfig{Listen: ":8411", Network: NetworkConfig{Timeout: 5 * time.Second}}},
	}
}

// settings resolves configuration into media's complete playback policy.
func (c CastConfig) settings() *mediav1.PlaybackSettings {
	subtitles := &castorv1.SubtitleSelection{}
	if len(c.Subtitles.Languages) > 0 {
		subtitles.Mode = &castorv1.SubtitleSelection_Preferences{Preferences: &castorv1.SubtitlePreferences{Languages: c.Subtitles.Languages, TranscribeIfMissing: c.Subtitles.TranscribeIfMissing}}
	} else {
		subtitles.Mode = &castorv1.SubtitleSelection_Disabled{Disabled: &emptypb.Empty{}}
	}
	settings := &mediav1.PlaybackSettings{
		Delivery:  castorv1.Delivery(c.Delivery),
		MaxHeight: c.MaxHeight,
		Subtitles: subtitles,
	}
	if c.MaxBitrate > 0 {
		settings.MaxBitrate = &mediav1.Bitrate{BitsPerSecond: uint64(c.MaxBitrate)}
	}
	if len(c.Audio.Languages) > 0 {
		settings.Audio = &castorv1.AudioSelection{Languages: c.Audio.Languages}
	}
	return settings
}
