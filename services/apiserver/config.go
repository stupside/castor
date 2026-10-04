package apiserver

import (
	"strings"
	"time"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/apiserver/internal/device/roku"
)

// Config is the API server's sections of castor's configuration.
type Config struct {
	Cast   CastConfig   `yaml:"cast"`
	API    APIConfig    `yaml:"api" validate:"required"`
	Server ServerConfig `yaml:"server"`
	// Scraping names only the resolver endpoint and credentials, never browser settings.
	Scraping ServerConfig  `yaml:"scraping"`
	Network  NetworkConfig `yaml:"network" validate:"required"`
	Devices  DevicesConfig `yaml:"devices"`
}

// CastConfig is what every cast asks unless its request says otherwise; New holds it to the contract's rules.
type CastConfig struct {
	Delivery  string `yaml:"delivery"`
	MaxHeight uint32 `yaml:"max_height"`
	// Subtitles is the language to burn in, a whisper code or auto to detect it, empty for none.
	Subtitles string `yaml:"subtitles"`
}

// APIConfig is where `castor api-server` listens, and the token it asks every request to carry.
type APIConfig struct {
	Listen string `yaml:"listen" validate:"required,hostname_port|startswith=:"`
	Token  string `yaml:"token"`
}

// ServerConfig is the media server this API server casts through; unset, castor runs one beside it in its own process.
type ServerConfig struct {
	URL   string `yaml:"url" validate:"omitempty,http_url"`
	Token string `yaml:"token"`
}

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
		Cast:    CastConfig{Delivery: "auto", MaxHeight: 1080},
		API:     APIConfig{Listen: ":8411"},
		Network: NetworkConfig{Timeout: 5 * time.Second},
	}
}

// preferences is the cast section as the contract asks it; a delivery the contract does not name is left unspecified, which it refuses.
func (c CastConfig) preferences() *castorv1.Preferences {
	return &castorv1.Preferences{
		Delivery:  castorv1.Delivery(castorv1.Delivery_value["DELIVERY_"+strings.ToUpper(c.Delivery)]).Enum(),
		MaxHeight: new(c.MaxHeight),
		Subtitles: new(c.Subtitles),
	}
}
