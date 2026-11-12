package mediaserver

import (
	"github.com/stupside/castor/internal/transport"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/source/rank"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle/whisper"
)

// Config is the media server's sections of castor's configuration.
type Config struct {
	Servers struct {
		Media ServerConfig `yaml:"media" settings:"strict" validate:"required"`
	} `yaml:"servers" validate:"required"`
}

// FFmpegConfig names the installed media tools.
type FFmpegConfig struct {
	Path             string            `yaml:"path" validate:"required"`
	ProbePath        string            `yaml:"probe_path" validate:"required"`
	HardwareEncoders []ffmpeg.Declared `yaml:"hardware_encoders"`
}

// ResolverConfig is how found streams are ranked.
type ResolverConfig struct {
	rank.Config `yaml:",inline"`
}

// ServerConfig is where `castor media-server` listens, where devices reach it, and the token its API asks for.
type ServerConfig struct {
	transport.Endpoint `yaml:",inline"`
	Listen             string `yaml:"listen" validate:"required,hostname_port|startswith=:"`
	// Advertise is where devices reach this server from outside its network.
	Advertise string `yaml:"advertise" validate:"omitempty,http_url"`
	// FFmpeg names the tools installed on this host.
	FFmpeg FFmpegConfig `yaml:"ffmpeg" validate:"required"`
	// Whisper configures transcription performed by this media server.
	Whisper  whisper.Config `yaml:"whisper"`
	Resolver ResolverConfig `yaml:"resolver" validate:"required"`
	Network  NetworkConfig  `yaml:"network"`
}

// NetworkConfig is the interface devices reach this machine on.
type NetworkConfig struct {
	Interface string `yaml:"interface"`
}

func defaults() Config {
	c := Config{}
	c.Servers.Media.FFmpeg = FFmpegConfig{Path: "ffmpeg", ProbePath: "ffprobe"}
	c.Servers.Media.Listen = ":8410"
	c.Servers.Media.Resolver = ResolverConfig{ProbeMaxConcurrency: 2}
	return c
}
