package mediaserver

import (
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/source/rank"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle/whisper"
)

// Config is the media server's sections of castor's configuration.
type Config struct {
	Resolver  ResolverConfig  `yaml:"resolver" validate:"required"`
	Transcode TranscodeConfig `yaml:"transcode" validate:"required"`
	Server    ServerConfig    `yaml:"server" validate:"required"`
	Network   NetworkConfig   `yaml:"network"`
	Whisper   whisper.Config  `yaml:"whisper"`
}

// TranscodeConfig is the ffmpeg binary, and how long one upstream read may stall.
type TranscodeConfig struct {
	FFmpegPath string        `yaml:"ffmpeg_path" validate:"required"`
	RWTimeout  time.Duration `yaml:"rw_timeout" validate:"required"`
}

// ResolverConfig is how found streams are read, measured and ranked.
type ResolverConfig struct {
	rank.Config `yaml:",inline"`

	// PlaylistTimeout bounds each HLS or DASH document fetch.
	PlaylistTimeout time.Duration `yaml:"playlist_timeout" validate:"required"`
	FFprobePath     string        `yaml:"ffprobe_path" validate:"required"`
	ProbeTimeout    time.Duration `yaml:"probe_timeout" validate:"required"`
}

// ServerConfig is where `castor media-server` listens, where devices reach it, and the token its API asks for.
type ServerConfig struct {
	Listen string `yaml:"listen" validate:"required,hostname_port|startswith=:"`
	// Advertise is where devices reach this server from outside its network.
	Advertise string `yaml:"advertise" validate:"omitempty,http_url"`
	// Token is what its API asks every request to carry; devices fetch what it serves without one.
	Token string `yaml:"token"`
}

// NetworkConfig is the interface devices reach this machine on.
type NetworkConfig struct {
	Interface string `yaml:"interface"`
}

func defaults() Config {
	return Config{
		Resolver: ResolverConfig{
			Config:          rank.Config{ProbeMaxConcurrency: 2},
			PlaylistTimeout: 30 * time.Second,
			FFprobePath:     "ffprobe",
			ProbeTimeout:    30 * time.Second,
		},
		Transcode: TranscodeConfig{FFmpegPath: "ffmpeg", RWTimeout: 30 * time.Second},
		Server:    ServerConfig{Listen: ":8410"},
	}
}
